#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: 2026 evroc
# Executed INSIDE the image by virt-customize --run, not on the build host.
set -euo pipefail

KUBERNETES_VERSION=$(cat /tmp/kubernetes-version)
[[ "$KUBERNETES_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  echo "Expected an exact Kubernetes version, e.g. v1.35.8" >&2
  exit 1
}
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ca-certificates curl gpg cloud-init containerd conntrack socat ebtables ethtool
mkdir -p /etc/containerd /etc/apt/keyrings /etc/modules-load.d /etc/sysctl.d
containerd config default > /etc/containerd/config.toml
sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
cat > /etc/modules-load.d/kubernetes.conf <<'EOF'
overlay
br_netfilter
EOF
cat > /etc/sysctl.d/99-kubernetes.conf <<'EOF'
net.bridge.bridge-nf-call-iptables = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward = 1
EOF
# Swap must remain disabled when clones first boot.
sed -i -E '/[[:space:]]swap[[:space:]]/s/^/#/' /etc/fstab
KUBE_MINOR="${KUBERNETES_VERSION%.*}"
curl -fsSL "https://pkgs.k8s.io/core:/stable:/$KUBE_MINOR/deb/Release.key" \
  | gpg --dearmor --yes -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
printf 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/%s/deb/ /\n' "$KUBE_MINOR" \
  > /etc/apt/sources.list.d/kubernetes.list
apt-get update
KUBE_PKG_VERSION=$(apt-cache madison kubeadm | awk -v v="${KUBERNETES_VERSION#v}-" 'index($3, v) == 1 && !found { print $3; found=1 }')
test -n "$KUBE_PKG_VERSION"
apt-get install -y "kubeadm=$KUBE_PKG_VERSION" "kubelet=$KUBE_PKG_VERSION" "kubectl=$KUBE_PKG_VERSION"
apt-mark hold kubeadm kubelet kubectl
# Optionally import a container archive before kubelet starts on each clone.
if [[ -f /tmp/container-images.tar ]]; then
  mkdir -p /var/lib/custom-image
  mv /tmp/container-images.tar /var/lib/custom-image/container-images.tar
  cat > /etc/systemd/system/custom-image-import.service <<'EOF'
[Unit]
Description=Import baked container images
Requires=containerd.service
After=containerd.service
Before=kubelet.service
ConditionPathExists=/var/lib/custom-image/container-images.tar

[Service]
Type=oneshot
ExecStart=/usr/bin/ctr --namespace k8s.io images import /var/lib/custom-image/container-images.tar
ExecStartPost=/usr/bin/rm /var/lib/custom-image/container-images.tar
RemainAfterExit=yes
TimeoutStartSec=10min

[Install]
WantedBy=multi-user.target
EOF
  mkdir -p /etc/systemd/system/kubelet.service.d
  printf '[Unit]\nRequires=custom-image-import.service\nAfter=custom-image-import.service\n' \
    > /etc/systemd/system/kubelet.service.d/20-custom-image-import.conf
  systemctl enable custom-image-import.service
fi
systemctl enable containerd kubelet
mkdir -p /usr/share/custom-image
dpkg-query -W > /usr/share/custom-image/packages.txt
apt-get clean
rm -f /tmp/kubernetes-version
