# Single-stage image: goreleaser injects the pre-built binary
# into the Docker build context at `linux/<arch>/<binary>` (one
# per platform goreleaser asked for). This Dockerfile copies the
# matching arch and ships it on distroless static.
#
# The version is already baked into the binary by goreleaser's
# -ldflags injection, so this Dockerfile needs no build args.

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

# TARGETARCH is set by `docker buildx --platform=linux/arm64` etc.
# Goreleaser arranges the build context as linux/amd64/ragabast,
# linux/arm64/ragabast, etc.
ARG TARGETARCH
COPY linux/${TARGETARCH}/ragabast /usr/local/bin/ragabast
# Tailwind CSS and daisyUI (both MIT) and htmx
# (BSD-2-Clause) are embedded in the binary via go:embed;
# their license texts must ship with every redistribution
# to satisfy the upstream terms. BSD-2 specifically
# requires the copyright notice in "the documentation
# and/or other materials provided with the distribution"
# — for a Docker image the standard location is
# /usr/share/doc/<package>/, which downstream scanners
# (e.g. syft, grype) also pick up automatically.
COPY THIRD_PARTY_LICENSES.md /usr/share/doc/ragabast/THIRD_PARTY_LICENSES.md

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/ragabast"]

# ragabast serve defaults to :8080; EXPOSE is documentation, not a
# hard binding, but it shows up in `docker inspect` and `docker scan`.
EXPOSE 8080

# OCI image metadata. Goreleaser adds equivalent labels via the
# `labels` block in .goreleaser.yml; these act as fallbacks for
# anyone building the image directly with `docker build`.
LABEL org.opencontainers.image.source="https://github.com/inful/ragabast"
LABEL org.opencontainers.image.licenses="MIT"
