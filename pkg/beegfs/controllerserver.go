/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

/*
Modifications Copyright 2021 NetApp, Inc. All Rights Reserved.
Licensed under the Apache License, Version 2.0.
*/

package beegfs

import (
	"path"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/glog"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/utils/mount"
)

var (
	// controllerCaps represents the capability of controller service
	controllerCaps = []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	}
)

type controllerServer struct {
	ctlExec                beegfsCtlExecutorInterface
	caps                   []*csi.ControllerServiceCapability
	nodeID                 string
	pluginConfig           pluginConfig
	clientConfTemplatePath string
	mounter                mount.Interface
	csDataDir              string
}

func NewControllerServer(nodeID string, pluginConfig pluginConfig, clientConfTemplatePath, csDataDir string) *controllerServer {
	return &controllerServer{
		ctlExec: &beegfsCtlExecutor{},
		caps: getControllerServiceCapabilities(
			[]csi.ControllerServiceCapability_RPC_Type{
				csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
			}),
		nodeID:                 nodeID,
		pluginConfig:           pluginConfig,
		clientConfTemplatePath: clientConfTemplatePath,
		csDataDir:              csDataDir,
		mounter:                nil,
	}
}

// CreateVolume generates a new volumeID and uses beegfs-ctl to create an associated directory at the proper location
// on the referenced BeeGFS file system. CreateVolume uses beegfs-ctl instead of mounting the file system and using
// mkdir because it needs to be able to use beegfs-ctl to set stripe patterns, etc. anyway.
func (cs *controllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	// Check arguments.
	volName := req.GetName()
	if len(volName) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume name not provided")
	}
	volCaps := req.GetVolumeCapabilities()
	if len(volCaps) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume capabilities not provided")
	}
	if valid, reason := isValidVolumeCapabilities(volCaps); !valid {
		return nil, status.Errorf(codes.InvalidArgument, "Volume capabilities not supported: %s", reason)
	}
	reqParams := req.GetParameters()
	if len(reqParams) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "Request parameters not provided")
	}
	sysMgmtdHost, ok := reqParams[sysMgmtdHostKey]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "%s not provided", sysMgmtdHostKey)
	}
	volDirBasePathBeegfsRoot, ok := reqParams[volDirBasePathKey]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "%s not provided", volDirBasePathKey)
	}
	volDirBasePathBeegfsRoot = path.Clean(path.Join("/", volDirBasePathBeegfsRoot))
	stripePatternConfig, err := getStripePatternParamsFromRequest(reqParams)
	if err != nil {
		return nil, newGrpcErrorFromCause(codes.InvalidArgument, err)
	}

	vol := cs.newBeegfsVolume(sysMgmtdHost, volDirBasePathBeegfsRoot, volName)

	// Write configuration files but do not mount BeeGFS.
	defer func() {
		// Failure to clean up is an internal problem. The CO only cares whether or not we created the volume.
		if err := cleanUpIfNecessary(vol, true); err != nil {
			glog.Warningf("Failed to clean up %s for %s: %+v", vol.mountDirPath, vol.volumeID, err)
		}
	}()
	if err := fs.MkdirAll(vol.mountDirPath, 0750); err != nil {
		err = errors.WithStack(err)
		return nil, newGrpcErrorFromCause(codes.Internal, err)
	}
	if err := writeClientFiles(vol, cs.clientConfTemplatePath); err != nil {
		return nil, newGrpcErrorFromCause(codes.Internal, err)
	}

	if err := cs.ctlExec.createDirectoryForVolume(vol); err != nil {
		return nil, newGrpcErrorFromCause(codes.Internal, err)
	}
	if err := cs.ctlExec.setPatternForVolume(vol, stripePatternConfig); err != nil {
		return nil, newGrpcErrorFromCause(codes.Internal, err)
	}

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId: vol.volumeID,
		},
	}, nil
}

// DeleteVolume deletes the directory referenced in the volumeID from the BeeGFS file system referenced in the
// volumeID.
func (cs *controllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	// Check arguments.
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "Volume ID not provided")
	}

	vol, err := cs.newBeegfsVolumeFromID(volumeID)
	if err != nil {
		return nil, newGrpcErrorFromCause(codes.Internal, err)
	}

	// Write configuration files and mount BeeGFS.
	defer func() {
		// Failure to clean up is an internal problem. The CO only cares whether or not we deleted the volume.
		if err := unmountAndCleanUpIfNecessary(vol, true, cs.mounter); err != nil {
			glog.Warningf("Failed to clean up %s for %s: %+v", vol.mountDirPath, vol.volumeID, err)
		}
	}()
	if err := fs.MkdirAll(vol.mountDirPath, 0750); err != nil {
		err = errors.WithStack(err)
		return nil, newGrpcErrorFromCause(codes.Internal, err)
	}
	if err := writeClientFiles(vol, cs.clientConfTemplatePath); err != nil {
		return nil, newGrpcErrorFromCause(codes.Internal, err)
	}
	if err := mountIfNecessary(vol, cs.mounter); err != nil {
		return nil, newGrpcErrorFromCause(codes.Internal, err)
	}

	// Delete volume from mounted BeeGFS.
	glog.V(LogDebug).Infof("Deleting BeeGFS directory %s for %s", vol.volDirBasePathBeegfsRoot, vol.volumeID)
	if err = fs.RemoveAll(vol.volDirPath); err != nil {
		err = errors.WithStack(err)
		return nil, newGrpcErrorFromCause(codes.Internal, err)
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (cs *controllerServer) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	var caps []*csi.ControllerServiceCapability
	for _, cap := range controllerCaps {
		c := &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: cap,
				},
			},
		}
		caps = append(caps, c)
	}
	return &csi.ControllerGetCapabilitiesResponse{Capabilities: caps}, nil
}

func (cs *controllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	// Check arguments.
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID not provided")
	}
	volCaps := req.GetVolumeCapabilities()
	if len(volCaps) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume capabilities not provided")
	}

	vol, err := cs.newBeegfsVolumeFromID(volumeID)
	if err != nil {
		return nil, newGrpcErrorFromCause(codes.Internal, err)
	}

	// Write configuration files but do not mount BeeGFS.
	defer func() {
		// Failure to clean up is an internal problem. The CO only cares whether or not the volume exists.
		if err := cleanUpIfNecessary(vol, true); err != nil {
			glog.Warningf("Failed to clean up %s for %s: %+v", vol.mountDirPath, vol.volumeID, err)
		}
	}()
	if err := fs.MkdirAll(vol.mountDirPath, 0750); err != nil {
		err = errors.WithStack(err)
		return nil, newGrpcErrorFromCause(codes.Internal, err)
	}
	if err := writeClientFiles(vol, cs.clientConfTemplatePath); err != nil {
		return nil, newGrpcErrorFromCause(codes.Internal, err)
	}

	if _, err := cs.ctlExec.statDirectoryForVolume(vol); err != nil {
		if errors.As(err, &ctlNotExistError{}) {
			return nil, newGrpcErrorFromCause(codes.NotFound, err)
		}
		return nil, newGrpcErrorFromCause(codes.Internal, err)
	}

	confirmed, reason := isValidVolumeCapabilities(volCaps)
	if confirmed {
		return &csi.ValidateVolumeCapabilitiesResponse{
			Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
				// VolumeContext: req.GetVolumeContext(),  // Our volumes do not include a context.
				VolumeCapabilities: volCaps,
				// TODO(webere, A142) Validate CreateVolumeRequest.parameters if provided.
				// Parameters: req.GetParameters(),
			},
		}, nil
	} else {
		return &csi.ValidateVolumeCapabilitiesResponse{
			Message: reason,
		}, nil
	}
}

func (cs *controllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (cs *controllerServer) ControllerGetVolume(ctx context.Context, in *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func getControllerServiceCapabilities(cl []csi.ControllerServiceCapability_RPC_Type) []*csi.ControllerServiceCapability {
	var csc []*csi.ControllerServiceCapability

	for _, cap := range cl {
		glog.V(LogDebug).Infof("Enabling controller service capability: %v", cap.String())
		csc = append(csc, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: cap,
				},
			},
		})
	}

	return csc
}

func getStripePatternParamsFromRequest(reqParams map[string]string) (stripePatternConfig, error) {
	stripePattern := stripePatternConfig{}
	for param := range reqParams {
		if strings.Contains(param, "stripePattern/") {
			switch param {
			case storagePoolIDKey:
				stripePattern.storagePoolID = reqParams[storagePoolIDKey]
			case stripePatternChunkSizeKey:
				stripePattern.stripePatternChunkSize = reqParams[stripePatternChunkSizeKey]
			case stripePatternNumTargetsKey:
				stripePattern.stripePatternNumTargets = reqParams[stripePatternNumTargetsKey]
			default:
				return stripePattern, errors.Errorf("CreateVolume parameter invalid: %s", param)
			}
		}
	}

	return stripePattern, nil
}

// (*controllerServer) newBeegfsVolume is a wrapper around newBeegfsVolume that makes it easier to call in the context
// of the controller service. (*controllerServer) newBeegfsVolume selects the mountDirPath and passes the controller
//service's pluginConfig.
func (cs *controllerServer) newBeegfsVolume(sysMgmtdHost, volDirBasePathBeegfsRoot, volName string) beegfsVolume {
	volDirPathBeegfsRoot := path.Join(volDirBasePathBeegfsRoot, volName)
	// This volumeID construction duplicates the one further down in the stack. We do it anyway to generate an
	// appropriate mountDirPath.
	volumeID := newBeegfsUrl(sysMgmtdHost, volDirPathBeegfsRoot)
	mountDirPath := path.Join(cs.csDataDir, sanitizeVolumeID(volumeID)) // e.g. /csDataDir/127.0.0.1_scratch_pvc-12345678
	return newBeegfsVolume(mountDirPath, sysMgmtdHost, volDirPathBeegfsRoot, cs.pluginConfig)
}

// (*controllerServer) newBeegfsVolumeFromID is a wrapper around newBeegfsVolumeFromID that makes it easier to call in
// the context of the controller service. (*controllerServer) newBeegfsVolumeFromID selects the mountDirPath and passes
// the controller service's pluginConfig.
func (cs *controllerServer) newBeegfsVolumeFromID(volumeID string) (beegfsVolume, error) {
	mountDirPath := path.Join(cs.csDataDir, sanitizeVolumeID(volumeID)) // e.g. /csDataDir/127.0.0.1_scratch_pvc-12345678
	return newBeegfsVolumeFromID(mountDirPath, volumeID, cs.pluginConfig)
}
