FROM fedora:32 AS builder

env LIBGUESTFS_BACKEND direct

RUN dnf update -y && dnf install --setopt=install_weak_deps=False -y \
    libguestfs  \
    libguestfs-devel \
    && dnf clean all

RUN mkdir -p /output \
    && libguestfs-make-fixed-appliance /output

FROM scratch 

COPY --from=builder /output/* /appliance/
