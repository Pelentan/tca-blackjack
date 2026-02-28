FROM alpine:3.19

RUN apk add --no-cache openssl bash

COPY gen-certs.sh /gen-certs.sh
RUN chmod +x /gen-certs.sh

VOLUME ["/certs"]

ENTRYPOINT ["/gen-certs.sh"]
