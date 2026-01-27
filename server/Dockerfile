FROM golang:1.24-bullseye AS builder
WORKDIR /src
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/resume-generator ./cmd/server

FROM ubuntu:22.04
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates wget gnupg2 apt-transport-https fonts-liberation libappindicator3-1 xdg-utils && rm -rf /var/lib/apt/lists/* \
 && wget -q -O - https://dl-ssl.google.com/linux/linux_signing_key.pub | apt-key add - \
 && echo "deb [arch=amd64] http://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list \
 && apt-get update && apt-get install -y --no-install-recommends google-chrome-stable && rm -rf /var/lib/apt/lists/*
ENV CHROME_PATH=/usr/bin/google-chrome-stable
WORKDIR /app
COPY --from=builder /out/resume-generator /app/resume-generator
COPY templates /app/templates
VOLUME ["/app/resume-data"]
EXPOSE 3000
ENTRYPOINT ["/app/resume-generator"]
