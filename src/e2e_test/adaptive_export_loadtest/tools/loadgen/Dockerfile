# Build both load-test binaries (cleanloadgen + httpsink) into one small image.
# Built on the PG dev-machine (native amd64 docker), never on the agent VM
# (ARM, no bazel/heavy-build) — same path used for the dx images.
FROM golang:1.22-bookworm AS build
WORKDIR /src
# -mod=mod lets the build resolve + record go.sum for the single lib/pq dep
# without a pre-committed go.sum (keeps the tool tree minimal).
ENV GOFLAGS=-mod=mod CGO_ENABLED=0
COPY go.mod ./
COPY cmd ./cmd
RUN go build -trimpath -ldflags="-s -w" -o /out/cleanloadgen ./cmd/cleanloadgen
RUN go build -trimpath -ldflags="-s -w" -o /out/httpsink     ./cmd/httpsink

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/cleanloadgen /usr/local/bin/cleanloadgen
COPY --from=build /out/httpsink     /usr/local/bin/httpsink
# Default to the sink; the generator pod overrides command to cleanloadgen.
ENTRYPOINT ["/usr/local/bin/httpsink"]
