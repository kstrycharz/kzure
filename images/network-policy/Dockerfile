FROM alpine:3.23@sha256:ff7f0c727dd796aa36787bfcd9a64bf35a6ce72a73451b3359fde849b6703d63
RUN apk add --no-cache iptables iproute2 conntrack-tools && mkdir -p /run/kzure
COPY policy.sh /usr/local/bin/kzure-network-policy
RUN chmod 755 /usr/local/bin/kzure-network-policy
ENTRYPOINT ["/usr/local/bin/kzure-network-policy"]
