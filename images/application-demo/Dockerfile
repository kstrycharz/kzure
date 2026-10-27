FROM node:24-alpine@sha256:06d7adf3e0ef61a13f1d28682726366f97a15aa1888c934d3ecec86c33918b53
ARG DEMO_VERSION=1
ENV DEMO_VERSION=$DEMO_VERSION
WORKDIR /app
COPY server.cjs ./
RUN mkdir -p /data && chown node:node /data
USER node
EXPOSE 3000
CMD ["node", "/app/server.cjs"]
