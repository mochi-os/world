# Mochi world server image. Built from pre-staged, host-cross-compiled
# binaries (make docker-stage) — the Dockerfile only COPYs; no compilation
# happens in the container build.
#
# Deliberate deltas from mochi-server's image: the nonroot base (world has no
# privilege-drop code and needs none — port 4433 is unprivileged and the only
# writable path is the ACME cache volume), and no HEALTHCHECK (world has no
# mochictl analog and distroless has no shell).
#
# TLS in the container: use [tls] with a mounted certificate/key — the standard
# container pattern. The built-in [acme] mode binds port 80 for HTTP-01, which
# the nonroot image cannot do (unlike the systemd package, which grants
# CAP_NET_BIND_SERVICE); running ACME here would need --cap-add=NET_BIND_SERVICE
# plus a lowered net.ipv4.ip_unprivileged_port_start, or a fronting proxy. Mount
# certs instead.
# Pinned by digest, not by tag, so the same commit always builds the same image
# and a moved upstream tag cannot propagate silently. Manifest LIST digest: a
# per-architecture one would build amd64 and fail arm64. Bump deliberately at
# release; `make base-digest` reports whether the tag has moved, and the
# scheduled Trivy container scan is what makes a stale pin visible.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35
ARG TARGETARCH
COPY build/docker/bin/mochi-world-${TARGETARCH} /usr/sbin/mochi-world
COPY build/docker/world.conf                    /etc/mochi/world.conf
VOLUME /var/lib/mochi-world
EXPOSE 4433/tcp 4433/udp
ENTRYPOINT ["/usr/sbin/mochi-world"]
