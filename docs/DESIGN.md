# DESIGN

The evroc CAPI provider is a CAPI [infrastructure provider](https://cluster-api.sigs.k8s.io/user/concepts#infrastructure-provider)

![api-groups](apigroups.drawio.png)

The evroc CAPI provider is primarily responsible for reconciling resource types `EvrocCluster` and `EvrocMachine`.

The CAPI "cluster" resource will be reconciled by the default CAPI controller. The [InfraCluster resource contract](https://cluster-api.sigs.k8s.io/developer/providers/contracts/infra-cluster) defines a set of rules a provider is expected to comply with in order to allow the expected interactions with the Cluster controller.

For evroc clusters, the reconciler:
- Creates a load balancer (evroc load balancer, L4 route, backend service, backend pool, public IP), and SGs (one for control plane nodes, one for worker nodes), and then reports ready


The `EvrocMachine` resource needs to comply with the contract defined by [CAPI](https://cluster-api.sigs.k8s.io/developer/core/controllers/machine). For each evroc machine, the reconciler:
- Creates an evroc VM and disk, attaching these VMs to the relevant security groups
- If this machine is a control plane node, it then adds it to the load balancer backend service
- When creating the VM, it passes down bootstrap configuration.

![overview](overview.drawio.png)
