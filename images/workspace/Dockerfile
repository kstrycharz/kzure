ARG DISTRO=ubuntu:24.04
FROM ${DISTRO}
LABEL org.opencontainers.image.title="Kzure Linux workspace"
RUN mkdir -p /data && chown 1000:1000 /data
USER 1000:1000
WORKDIR /data
CMD ["sleep", "infinity"]
