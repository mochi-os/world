# Mochi world server image: COPYs pre-staged cross-compiled binaries (make
# docker-stage). Use [tls] with a mounted certificate - the nonroot base cannot
# bind port 80 for ACME. Pin the base by manifest-list digest, never by tag.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35
ARG TARGETARCH
COPY build/docker/bin/mochi-world-${TARGETARCH} /usr/sbin/mochi-world
COPY build/docker/world.conf                    /etc/mochi/world.conf
VOLUME /var/lib/mochi-world
EXPOSE 4433/tcp 4433/udp
ENTRYPOINT ["/usr/sbin/mochi-world"]
