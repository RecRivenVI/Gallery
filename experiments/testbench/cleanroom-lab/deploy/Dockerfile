FROM golang:1.26.5 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gallery-probe ./cmd/account

FROM scratch
COPY --from=build /out/gallery-probe /gallery-probe
EXPOSE 18096
ENTRYPOINT ["/gallery-probe", "-addr", "0.0.0.0:18096", "-mode", "lan"]
