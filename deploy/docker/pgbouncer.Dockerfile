FROM alpine:3.20
RUN apk add --no-cache pgbouncer \
 && adduser -D -H -u 1000 pgbouncer
USER pgbouncer
ENTRYPOINT ["pgbouncer", "/etc/pgbouncer/pgbouncer.ini"]
