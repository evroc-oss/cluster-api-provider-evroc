# evroc Cloud Infrastructure Provider for Cluster API

This chart deploys the evroc Cloud infrastructure provider for [Cluster API](https://cluster-api.sigs.k8s.io/), enabling you to provision and manage Kubernetes clusters on evroc's European sovereign cloud platform.

The provider supports both kubeadm and RKE2 bootstrap providers, integrates with Rancher Turtles for automatic cluster import, and includes per-cluster credentials for multi-tenant environments. Workload clusters are created declaratively using standard CAPI resources (`EvrocCluster`, `EvrocMachine`, `EvrocMachineTemplate`).
