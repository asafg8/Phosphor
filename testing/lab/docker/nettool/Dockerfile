# phosphor-lab nettool: runs in a fixture's network namespace (docker run --net container:<fx>) to add iptables rules
# without touching the fixture's filesystem (lab block-host / offline).
FROM alpine:3.20
RUN apk add --no-cache iptables bind-tools
