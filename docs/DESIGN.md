# DESIGN

The evroc CAPI provider is a CAPI [infrastructure provider](https://cluster-api.sigs.k8s.io/user/concepts#infrastructure-provider)

![api-groups](apigroups.drawio.png)

The evroc CAPI provider is primarily responsible for reconciling resource types `EvrocCluster` and `EvrocMachine`.

The CAPI "cluster" resource will be reconciled by the default CAPI controller. The [InfraCluster resource contract](https://cluster-api.sigs.k8s.io/developer/providers/contracts/infra-cluster) defines a set of rules a provider is expected to comply with in order to allow the expected interactions with the Cluster controller.

For evroc clusters, the reconciler:
- Creates SGs for control plane and worker nodes.
- When `spec.controlPlaneEndpointPolicy` is `Managed` (the default), creates the control plane load balancer and its supporting resources. With `External`, another component owns the endpoint and CAPE does not create or delete load balancer resources.


The `EvrocMachine` resource needs to comply with the contract defined by [CAPI](https://cluster-api.sigs.k8s.io/developer/core/controllers/machine). For each evroc machine, the reconciler:
- Creates an evroc VM and disk, attaching these VMs to the relevant security groups
- If this machine is a control plane node and CAPE owns the endpoint, it adds it to the load balancer backend service.
- When creating the VM, it passes down bootstrap configuration.

![overview](overview.drawio.png)
