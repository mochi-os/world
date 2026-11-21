# Mochi world server image: COPYs pre-staged cross-compiled binaries (make
# docker-stage). Either TLS mode works: [tls] with a mounted certificate, or
# [acme], whose responder binds port 80 as the nonroot user. Docker 20.10 and
# later let a container's processes bind any port; another runtime may need
# net.ipv4.ip_unprivileged_port_start=0 set for the container. Pin the base by
# manifest-list digest, never by tag.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35
ARG TARGETARCH
COPY build/docker/bin/mochi-world-${TARGETARCH} /usr/sbin/mochi-world
COPY build/docker/world.conf                    /etc/mochi/world.conf
# Owned by the runtime user before VOLUME declares it. A mount point created by
# VOLUME alone is root 0755, and the nonroot base runs as 65532, so on the
# default anonymous volume world.id could not be written (listing_id returns ""
# and listing goes quietly off) and autocert could not cache a certificate.
COPY --chown=65532:65532 build/docker/state      /var/lib/mochi-world
VOLUME /var/lib/mochi-world
# 80 answers ACME's HTTP-01 challenges, and only when [acme] hosts is set.
EXPOSE 4433/tcp 4433/udp 80/tcp
ENTRYPOINT ["/usr/sbin/mochi-world"]
