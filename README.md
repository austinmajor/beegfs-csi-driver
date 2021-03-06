# BeeGFS CSI Driver

## Contents 
* [Overview](#overview)
* [Getting Started](#getting-started)
* [Basic Use and Examples](#basic-use)
* [Requesting Enhancements and Reporting
  Issues](#requesting-enhancements-and-reporting-issues)
* [License](#license)
* [Maintainers](#maintainers)

## Overview 

The BeeGFS Container Storage Interface (CSI) driver provides high performing and
scalable storage for workloads running in container orchestrators like
Kubernetes. This driver allows containers to access existing datasets or request
on-demand ephemeral or persistent high speed storage backed by [BeeGFS parallel
file systems](https://blog.netapp.com/beegfs-for-beginners/). 

### Notable Features

* Integration of Storage Classes in Kubernetes with [storage
  pools](https://doc.beegfs.io/latest/advanced_topics/storage_pools.html) in
  BeeGFS, allowing different tiers of storage within the same file system to be
  exposed to end users. 
* Management of global and node specific BeeGFS client configuration applied to
  Kubernetes nodes, simplifying use in large environments. 
* Set [striping
  parameters](https://doc.beegfs.io/latest/advanced_topics/striping.html) in
  BeeGFS from Storage Classes in Kubernetes to optimize for diverse workloads
  sharing the same file system.
* Support for ReadWriteOnce, ReadOnlyMany, and ReadWriteMany [access
  modes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#access-modes)
  in Kubernetes allow workloads distributed across multiple Kubernetes nodes to
  share access to the same working directories and enable multi-user/application
  access to common datasets.

### Interoperability and CSI Feature Matrix
| beegfs.csi.netapp.com  | K8s Versions  | BeeGFS Versions | CSI Version  | Persistence | Supported Access Modes   | Dynamic Provisioning |
| -----------------------| ------------- | --------------- | ------------ | ----------- | ------------------------ | -------------------- |
| v1.0.0                 | 1.19          | 7.2, 7.1.5      | v1.3.0       | Persistent  | Read/Write Multiple Pods | Yes                  |  

Additional Notes:
* This matrix indicates tested BeeGFS and Kubernetes versions. The driver is
  expected to work with other versions of Kubernetes, but extensive testing has
  not been performed, and changes to the deployment manifests are required.
* The driver has not been tested with SELinux.

## Getting Started 

### Prerequisite(s) 

* Deploying the driver requires access to a terminal with kubectl. 
* The [BeeGFS DKMS
  client](https://doc.beegfs.io/latest/advanced_topics/client_dkms.html) must be
  preinstalled to each Kubernetes node that needs BeeGFS access. 
* Each BeeGFS mount point uses an ephemeral UDP port. On Linux the selected
  ephemeral port is constrained by the values of [IP
  variables](https://www.kernel.org/doc/html/latest/networking/ip-sysctl.html#ip-variables).
  [Ensure that firewalls allow UDP
  traffic](https://doc.beegfs.io/latest/advanced_topics/network_tuning.html#firewalls-network-address-translation-nat)
  between BeeGFS management/metadata/storage nodes and ephemeral ports on
  Kubernetes nodes.
* One or more existing BeeGFS file systems should be available to the Kubernetes
  nodes over a TCP/IP and/or RDMA (InfiniBand/RoCE) capable network (not
  required to deploy the driver).

### Quick Start
The steps in this section allow you to get the driver up and running quickly.
For production use cases or air-gapped environments it is recommended to read
through the full [deployment guide](docs/deployment.md). 

1. On a machine with kubectl and access to the Kubernetes cluster where you want
   to deploy the BeeGFS CSI driver clone this repository: `git clone
   https://github.com/NetApp/beegfs-csi-driver.git`
2. Change to the BeeGFS CSI driver directory (`cd beegfs-csi-driver`) and run:
   `kubectl apply -k deploy/prod`
    * Note by default the beegfs-csi-driver image will be pulled from
      [DockerHub](https://hub.docker.com/r/netapp/beegfs-csi-driver).
3. Verify all components are installed and operational: `kubectl get pods -n
   kube-system | grep csi-beegfs`

As a one-liner: `git clone https://github.com/NetApp/beegfs-csi-driver.git && cd
beegfs-csi-driver && kubectl apply -k deploy/prod && kubectl get pods -n
kube-system | grep csi-beegfs`

Provided all Pods are running the driver is now ready for use. See the following
sections for how to get started using the driver.

## Basic Use

 This section provides a quick summary of basic driver use and functionality.
 Please see the full [usage documentation](docs/usage.md) for a complete
 overview of all available functionality. The driver was designed to support
 both dynamic and static storage provisioning and allows directories in BeeGFS
 to be used as [Persistent
 Volumes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/) (PVs)
 in Kubernetes. Pods with Persistent Volume Claims (PVCs) are only able to
 see/access the specified directory (and any subdirectories), providing
 isolation between multiple applications and users using the same BeeGFS file
 system when desired. 

### Dynamic Storage Provisioning:

Administrators create a Storage Class in Kubernetes referencing at minimum a
specific BeeGFS file system and parent directory within that file system. Users
can then submit PVCs against the Storage Class, and are provided isolated access
to new directories under the parent specified in the Storage Class. 

### Static Provisioning:

Administrators create a PV and PVC representing an existing directory in a
BeeGFS file system. This is useful for exposing some existing dataset or shared
directory to Kubernetes users and applications.

### Examples

[Example Kubernetes manifests](examples/README.md) of how to use the driver are
provided. These are meant to be repurposed to simplify creating objects related
to the driver including Storage Classes, Persistent Volumes, and Persistent
Volume Claims in your environment.

## Requesting Enhancements and Reporting Issues 

If you have any questions, feature requests, or would like to report an issue
please submit them at https://github.com/NetApp/beegfs-csi-driver/issues. 

## License 

Apache License 2.0

## Maintainers 

* Austin Major (@austinmajor).
* Eric Weber (@ejweber).
* Joe McCormick (@iamjoemccormick).
* Joey Parnell (@unwieldy0). 
* Justin Bostian (@jb5n).
