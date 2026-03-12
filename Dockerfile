FROM --platform=$BUILDPLATFORM golang:1.26.1 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ENV CGO_ENABLED=0
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X tofuhut/cmd.version=${VERSION} -X tofuhut/cmd.commit=${COMMIT}" -o /out/tofuhut .

FROM gcr.io/distroless/static-debian13:nonroot
WORKDIR /
COPY --from=build /out/tofuhut /tofuhut
USER nonroot:nonroot
ENTRYPOINT ["/tofuhut"]
