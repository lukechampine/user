FROM golang:alpine AS build
WORKDIR $GOPATH/src/github.com/lukechampine/user
COPY . .
ENV CGO_ENABLED=0
RUN apk -U --no-cache add bash upx git gcc make \
    && make static \
    && upx /go/bin/user

FROM scratch
COPY --from=build /go/bin/user /user
COPY --from=alpine:latest /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
CMD ["/user"]
