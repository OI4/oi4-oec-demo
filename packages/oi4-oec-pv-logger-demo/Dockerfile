FROM node:18-alpine

# -------INSTALL OPENSSL
RUN apk add --update openssl && rm -rf /var/cache/apk/*

# -------COPY resources
WORKDIR /usr
COPY ./build/container ./oi4-oec-pv-logger-demo

WORKDIR /usr/oi4-oec-pv-logger-demo

# COPY logs directory
RUN mkdir -p logs

LABEL org.opencontainers.image.source=https://github.com/oi4/oi4-oec-demo

RUN chmod +x "scripts/entrypoint.sh"
ENTRYPOINT ["scripts/entrypoint.sh"]
