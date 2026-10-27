FROM ubuntu:24.04@sha256:68ee22004a9109b44973313b02ff7b1de250f1de0f5ecb628d73f579aaff33ac
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends xfce4 xfce4-terminal xrdp xorgxrdp dbus-x11 ca-certificates openssl && rm -rf /var/lib/apt/lists/* && \
    usermod -l kzure -G '' -d /data/home/kzure -s /bin/bash ubuntu && \
    mkdir -p /run/kzure /data/home/kzure /var/run/dbus && chown -R 1000:1000 /data/home/kzure && \
    printf 'auth required pam_unix.so\naccount required pam_unix.so\nsession required pam_unix.so\n' > /etc/pam.d/xrdp-sesman && \
    printf '#!/bin/sh\nunset DBUS_SESSION_BUS_ADDRESS\nunset XDG_RUNTIME_DIR\nexec dbus-run-session -- startxfce4\n' > /etc/xrdp/startwm.sh && chmod 755 /etc/xrdp/startwm.sh && \
    sed -i 's/^AllowRootLogin=.*/AllowRootLogin=false/;s/^MaxSessions=.*/MaxSessions=4/' /etc/xrdp/sesman.ini && \
    sed -i 's/^security_layer=.*/security_layer=tls/;s/^crypt_level=.*/crypt_level=high/;s/^#\?tcp_nodelay=.*/tcp_nodelay=true/' /etc/xrdp/xrdp.ini
COPY start.sh /usr/local/bin/kzure-desktop-start
RUN chmod 755 /usr/local/bin/kzure-desktop-start
WORKDIR /data
EXPOSE 3389
ENTRYPOINT ["/usr/local/bin/kzure-desktop-start"]
