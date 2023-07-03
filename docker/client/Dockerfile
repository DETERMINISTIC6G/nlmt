FROM golang:1.19.3
RUN go install github.com/samiemostafavi/nlmt/cmd/irtt@latest

RUN mkdir -p /tmp/results
WORKDIR /tmp/results

COPY entrypoint.sh /tmp/entrypoint.sh
RUN chmod +x /tmp/entrypoint.sh
ENTRYPOINT ["/tmp/entrypoint.sh"]
