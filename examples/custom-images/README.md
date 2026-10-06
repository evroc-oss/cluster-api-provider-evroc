# Custom boot images

Use an existing evroc `CustomDiskImage` with any compatible bootstrap provider.
The infrastructure provider creates the boot disk and VM; it does not build,
upload, register, update or delete the source image. This feature requires the
custom disk image API to be enabled by evroc.

Custom images let customers preinstall software, apply their security baseline,
include monitoring agents, and cache dependencies for faster, more consistent
startup. This guide shows Kubernetes node images as one application of that
general capability.

In an `EvrocMachineTemplate`, set `image` to `custom:<name>`:

```yaml
spec:
  template:
    spec:
      project: my-project
      region: se-sto
      computeProfile: a1a.m
      image: custom:my-node-image-v1
      rootDiskSize: 50
```

The shorthand uses the machine's project and region. You can also supply the
full ref, `/compute/projects/my-project/regions/se-sto/customDiskImages/my-node-image-v1`.
Both forms work for root disks and `additionalDisks[].image`.

The ref must be in the machine's project and region; cross-project image
references are not accepted. `image` takes either a ref or an evroc image name.
The image is immutable on an existing `EvrocMachine`. To roll out a new image,
create a new `EvrocMachineTemplate` and update the control plane or
MachineDeployment's infrastructure reference. Keep old images available while
machines may still be created from their templates.

Existing standard-image templates and their Ubuntu default remain supported.
When upgrading the provider, apply its updated CRDs as well: Helm does not upgrade
existing CRDs in its `crds/` directory automatically.

## Image contract

Disk images accept stock names or full references.

The image must boot on the selected evroc compute profile and consume the user
data produced by your bootstrap provider. The current provider sends bootstrap
data through evroc's cloud-init user-data field; selecting an arbitrary OS does
not add support for a different bootstrap-data transport. Retain cloud-init and
the cloud image's networking/virtio support. Match the image architecture and
ensure the root disk is at least the image's virtual disk size.

Choose whatever packages, hardening, agents or cached container images your
workload needs. Keep machine identity, cluster certificates, bootstrap tokens,
cloud credentials and application secrets out of the image. Always test a fresh
clone, not just the build machine.

For Kubernetes, CSI host prerequisites and cached driver containers may be baked
in. Deploy CSI controllers, node DaemonSets, RBAC, StorageClasses and credentials
through your cluster add-on mechanism. An image alone does not install CSI into a
cluster, and storage readiness should include a PVC mount/read/write test.

## Example: bake an Ubuntu kubeadm image

This optional example moves package installation from first boot to image build
time. It is an adaptable recipe, not a provider requirement or a production
image release pipeline. It needs a Linux build host with `qemu-img`,
`virt-customize`, and `virt-sysprep` (Ubuntu packages `qemu-utils` and
`libguestfs-tools`), enough disk space, and network access for guest package
installation. Run it against a trusted, unused base image, never a running VM.

Download an amd64 Ubuntu 24.04 cloud image from
[Canonical](https://cloud-images.ubuntu.com/noble/) and verify its signed checksum.
Record the base image digest and use an immutable download in your own pipeline.
The example expects that base to be qcow2; verify the format before proceeding.
From the repository root:

```bash
set -e
export BASE_IMAGE=/path/to/verified-ubuntu-24.04-amd64.img
export OUTPUT_IMAGE=/path/to/my-node-image-v1.qcow2
export KUBERNETES_VERSION=v1.35.8 # choose a version supported by your CAPI stack

test ! -e "$OUTPUT_IMAGE"
qemu-img convert -f qcow2 -O qcow2 "$BASE_IMAGE" "$OUTPUT_IMAGE"
virt-customize --format qcow2 -a "$OUTPUT_IMAGE" \
  --write "/tmp/kubernetes-version:$KUBERNETES_VERSION" \
  --run examples/custom-images/provision-kubeadm.sh
virt-sysprep --format qcow2 -a "$OUTPUT_IMAGE" \
  --operations ssh-hostkeys,ssh-userdir,bash-history,logfiles,tmp-files
virt-customize --format qcow2 -a "$OUTPUT_IMAGE" \
  --run-command 'cloud-init clean --logs --machine-id --seed'
qemu-img check -f qcow2 "$OUTPUT_IMAGE"
sha256sum "$OUTPUT_IMAGE"
```

Run these commands in a shell with `set -e` so a failed build cannot proceed to
publication. The provisioner installs the requested Kubernetes patch version,
configures containerd and kernel settings, and records installed package versions
in `/usr/share/custom-image/packages.txt`. It does not initialize or join a cluster.
Ubuntu package versions still depend on repository contents; use snapshot
repositories and pin all packages for reproducible production builds. The optional
container archive below preloads images; this alone does not make a cluster air-gapped.

Boot-test two fresh copies and check unique machine IDs/SSH host keys, cloud-init,
networking, root filesystem growth and bootstrap. Compare disk creation-to-Node
Ready time against a stock image: larger images take longer to import, so removing
package installation does not guarantee a particular speedup.

The tools are documented in the upstream
[virt-customize manual](https://libguestfs.org/virt-customize.1.html),
[virt-sysprep manual](https://libguestfs.org/virt-sysprep.1.html), and
[cloud-init clean reference](https://docs.cloud-init.io/en/latest/reference/cli.html).

## Register and use it

Use the evroc CLI profile for the target project and region. Create a versioned
bucket and a bucket service account (BSA) with access to that bucket:

```bash
evroc config current-profile
evroc config current-project
# If necessary: evroc config set-project my-project
# Check the selected profile's region as well; it must match the cluster region.

evroc storage bucket create my-images --object-retention-mode Versioned
evroc storage bucket get my-images -o yaml
# Wait for the bucket to be ready before creating the BSA.

evroc storage bucketserviceaccount create image-reader --bucket my-images
evroc storage bucketserviceaccount get image-reader -o yaml
```

These creates are asynchronous. Repeat the `get` commands until the resources
are ready. A BSA is separate from the IAM service account used by CAPI. The
`--bucket` flag grants access to the named bucket; naming the account
`image-reader` does not make its permissions read-only.

Upload the standalone qcow2 file to `my-images` with the object key
`images/my-node-image-v1.qcow2`, using your S3 upload tooling and publisher
credentials. Record the uploaded object's version ID. If you need S3 credentials
for this BSA, its status exposes `s3CredentialsSecretName`; retrieve that secret
with `evroc storage bucketserviceaccountsecret get <secret-name>`. That command
prints credentials, so keep its output out of shared logs and manifests.

Register the image using a CLI version that includes `compute customdiskimage`
and an environment where the custom-image API is enabled:

```bash
evroc compute customdiskimage create my-node-image-v1 \
  --bucket=my-images \
  --bucket-service-account=image-reader \
  --path=images/my-node-image-v1.qcow2 \
  --version=OBJECT_VERSION_ID \
  --architecture=amd64 \
  --size-amount=50 --size-unit=GB
```

Use the actual object version ID, not the Kubernetes version or an image label.
Pin the object version and retain the checksum/build manifest externally; image
metadata immutability alone does not prevent replacement of an unversioned object.
Register/copy images per project where necessary. Image publishing credentials
belong to the publisher, not the CAPI controller. Grant the controller the platform
permissions needed to consume the image according to evroc's authorization policy.

For kubeadm, the `prebuilt` flavor removes first-boot package installation and
checks that the baked kubeadm/kubelet versions match `KUBERNETES_VERSION`:

```bash
export EVROC_IMAGE=custom:my-node-image-v1
# Also export the normal cluster template variables described in the root README.
clusterctl generate cluster my-cluster \
  --infrastructure evroc --flavor prebuilt \
  --kubernetes-version "$KUBERNETES_VERSION" \
  --control-plane-machine-count 3 --worker-machine-count 3 > cluster.yaml
```

The flavor ships with provider releases containing this change. While developing,
render `templates/cluster-template-prebuilt.yaml` with clusterctl's `--from` option.
It retains the default template's Cilium installation, which requires downloads;
it does not install a CSI driver or CCM. For other bootstrap providers, keep the
custom image ref in `image` and supply an appropriate bootstrap template. Existing
package-install commands are not automatically removed from user templates.

## Upgrading prebuilt clusters

Publish a new immutable image for every Kubernetes, OS or baked-package update.
For a Kubernetes upgrade, confirm compatibility with your CAPI and add-on
versions, upgrade one minor version at a time, and roll the control plane before
workers. The checks in `preKubeadmCommands` contain the version rendered when the
cluster was created; changing the image alone will not update those checks.

1. Create a new `EvrocMachineTemplate` selecting the new image.
2. In one control-plane update, change the `KubeadmControlPlane` infrastructure
   template reference, `spec.version`, and both kubeadm/kubelet version checks in
   `spec.kubeadmConfigSpec.preKubeadmCommands`. Wait for the rollout to finish.
3. Create a new worker `KubeadmConfigTemplate` with the matching version checks.
   In one `MachineDeployment` update, set the new infrastructure and bootstrap
   template references and the matching `spec.template.spec.version`.
4. Verify node readiness and required add-ons (including a storage smoke test
   when using CSI) before retiring old images and templates.

For an OS-only update, keep the Kubernetes version and bootstrap checks unchanged
and roll out the new infrastructure template reference. Retain previous images
as required for recovery, but do not assume Kubernetes minor-version downgrades
are supported. Image versioning does not replace cluster backup and recovery.

## Optional: preload container images

For faster bootstrap, put the container references used by your applications and
cluster add-ons in `images.txt`, one per line, including any sidecars. Derive
this list from the manifests or charts you intend to deploy. Use immutable tags or digests; image
references and architecture must match the deployed manifests.

On an amd64 Docker build host, create an archive:

```bash
set -e
mapfile -t images < images.txt
for image in "${images[@]}"; do docker pull --platform linux/amd64 "$image"; done
docker save -o container-images.tar "${images[@]}"
```

Add `--upload container-images.tar:/tmp/container-images.tar` to the
`virt-customize` invocation **before** `--run .../provision-kubeadm.sh` above.
The provisioner installs a one-time import service that loads the archive into
containerd's `k8s.io` namespace before kubelet starts, then removes the archive.
Use `imagePullPolicy: IfNotPresent` in the deployed workloads to use the cache.
The archive caches images; your deployment automation still installs and
configures the workloads.

CCM and CSI are optional examples of add-ons that can benefit from this cache.
When using an external CCM, configure `cloud-provider=external` before nodes
join. Deploy add-ons and inject credentials through your cluster automation;
verify node initialization and a PVC mount/read/write test when using CSI.
