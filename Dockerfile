# Mochi world server image. Built from pre-staged, host-cross-compiled
# binaries (make docker-stage) — the Dockerfile only COPYs; no compilation
# happens in the container build.
#
# Deliberate deltas from mochi-server's image: the nonroot base (world has no
# privilege-drop code and needs none — port 4433 is unprivileged and the only
# writable path is the ACME cache volume), and no HEALTHCHECK (world has no
# mochictl analog and distroless has no shell).
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETARCH
COPY build/docker/bin/mochi-world-${TARGETARCH} /usr/sbin/mochi-world
COPY build/docker/world.conf                    /etc/mochi/world.conf
VOLUME /var/lib/mochi-world
EXPOSE 4433/tcp 4433/udp
ENTRYPOINT ["/usr/sbin/mochi-world"]
