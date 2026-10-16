# A container that boots systemd (v260 or newer) as PID 1 for the
# integration tests. systemd-report ships in systemd itself on Fedora and
# in the systemd package on Arch.
ARG IMAGE=fedora:rawhide
FROM ${IMAGE}
RUN if command -v dnf >/dev/null; then \
        dnf -y install systemd systemd-networkd procps-ng iproute && dnf clean all; \
    else \
        pacman -Syu --noconfirm systemd procps-ng iproute2 && pacman -Scc --noconfirm; \
    fi \
    && systemctl mask systemd-firstboot.service getty.target console-getty.service \
    && systemctl enable systemd-networkd.service
STOPSIGNAL SIGRTMIN+3
CMD ["/usr/lib/systemd/systemd"]
