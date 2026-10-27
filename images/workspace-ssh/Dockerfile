ARG DISTRO=ubuntu:24.04
FROM ${DISTRO}
USER root
RUN set -eux; \
    if command -v apt-get >/dev/null; then apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends openssh-server passwd ca-certificates util-linux && rm -rf /var/lib/apt/lists/*; \
    elif command -v apk >/dev/null; then apk add --no-cache openssh-server openssh-client shadow util-linux ca-certificates; \
    elif command -v microdnf >/dev/null; then microdnf install -y openssh-server shadow-utils util-linux && microdnf clean all; \
    else dnf install -y openssh-server shadow-utils util-linux && dnf clean all; fi; \
    existing="$(getent passwd 1000 | cut -d: -f1)"; \
    if [ -n "$existing" ]; then usermod -l kzure -d /data/home/kzure -s /bin/sh "$existing"; \
    else getent group 1000 >/dev/null || groupadd -g 1000 kzure; useradd -u 1000 -g 1000 -d /data/home/kzure -s /bin/sh kzure; fi; \
    usermod -p '' -G '' kzure; mkdir -p /data /run/sshd /run/kzure; chown 1000:1000 /data; \
    rm -f /etc/ssh/ssh_host_*
COPY sshd_config /etc/ssh/sshd_config
COPY start.sh /usr/local/bin/kzure-ssh-start
RUN chmod 755 /usr/local/bin/kzure-ssh-start
WORKDIR /data
EXPOSE 22
ENTRYPOINT ["/usr/local/bin/kzure-ssh-start"]
