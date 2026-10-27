#!/usr/bin/env bash
# Observe php-fpm startup network behaviour WITHOUT contacting the real telemetry host.
# - Internal docker network (no internet). The telemetry hostname is mapped (/etc/hosts via --add-host) to a sink
#   container that only captures packets. Nothing leaves the laptop.
# - Case A (enabled): telemetry host -> sink. Capture UDP/53 at the sink during `systemctl restart php7.4-fpm`.
# - Case B (operator off-switch, hosts override): telemetry host -> 0.0.0.0. Capture all UDP/53 in the fpm container's
#   netns; time the restart.
# - Case C (unresolvable, e.g. DNS-level block / no egress DNS): no mapping on the internal network.
# Usage: capture.sh <out-dir>   (image: phosphor-lab/f2:local = ubuntu 22.04 + ppa:ondrej/php php7.4-fpm 1:7.4.33-31)
set -uo pipefail
OUT=${1:?out dir}; mkdir -p "$OUT"
IMG=phosphor-lab/f2:local; NS=nicolaka/netshoot:v0.13; NET=phosphor-tele-internal; SINK_IP=172.31.250.53
docker pull -q $NS >/dev/null
docker network rm $NET >/dev/null 2>&1; docker network create --internal --subnet 172.31.250.0/24 $NET >/dev/null
cleanup() { docker rm -f phosphor-tele-sink phosphor-tele-fpm >/dev/null 2>&1; docker network rm $NET >/dev/null 2>&1; }
trap cleanup EXIT

restart_timed() { docker exec phosphor-tele-fpm bash -c 's=$(date +%s%N); systemctl restart php7.4-fpm; e=$(date +%s%N); echo "restart_ms=$(( (e-s)/1000000 )) active=$(systemctl is-active php7.4-fpm)"'; }
start_fpm() { # $1 = --add-host value or empty
  docker rm -f phosphor-tele-fpm >/dev/null 2>&1
  docker run -d --name phosphor-tele-fpm --network $NET ${1:+--add-host "$1"} --privileged --cgroupns=host \
    -v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock $IMG >/dev/null
  for i in $(seq 1 60); do docker exec phosphor-tele-fpm systemctl is-active php7.4-fpm >/dev/null 2>&1 && break; sleep 1; done
}

echo "== Case A: telemetry.sury.org -> sink $SINK_IP (internal network, no internet)" | tee "$OUT/capture.txt"
docker run -d --name phosphor-tele-sink --network $NET --ip $SINK_IP $NS sleep 600 >/dev/null
start_fpm "telemetry.sury.org:$SINK_IP"
docker exec -d phosphor-tele-sink sh -c "tcpdump -n -l -i eth0 -s 0 -X udp port 53 > /tmp/a.txt 2>&1"; sleep 2
restart_timed | tee -a "$OUT/capture.txt"; sleep 3
docker exec phosphor-tele-sink sh -c 'pkill tcpdump; sleep 1; cat /tmp/a.txt' > "$OUT/caseA-tcpdump.txt"
grep -E '^[0-9]{2}:' "$OUT/caseA-tcpdump.txt" | sed 's/^/  /' | tee -a "$OUT/capture.txt"
docker exec phosphor-tele-fpm dpkg-query -W php7.4-fpm | sed 's/^/  package: /' | tee -a "$OUT/capture.txt"

echo "== Case B: hosts override telemetry.sury.org -> 0.0.0.0" | tee -a "$OUT/capture.txt"
start_fpm "telemetry.sury.org:0.0.0.0"
docker run -d --rm --name phosphor-tele-cap --net container:phosphor-tele-fpm $NS sh -c "tcpdump -n -l -i any udp port 53 > /tmp/b.txt 2>&1; sleep 600" >/dev/null; sleep 2
restart_timed | tee -a "$OUT/capture.txt"; sleep 3
docker exec phosphor-tele-cap sh -c 'pkill tcpdump; sleep 1; cat /tmp/b.txt' > "$OUT/caseB-tcpdump.txt"; docker rm -f phosphor-tele-cap >/dev/null
echo "  packets on any interface: $(grep -cE '^[0-9]{2}:' "$OUT/caseB-tcpdump.txt")" | tee -a "$OUT/capture.txt"
grep -E '^[0-9]{2}:' "$OUT/caseB-tcpdump.txt" | sed 's/^/  /' | tee -a "$OUT/capture.txt"

echo "== Case C: no mapping, internal network (name does not resolve)" | tee -a "$OUT/capture.txt"
start_fpm ""
docker run -d --rm --name phosphor-tele-cap --net container:phosphor-tele-fpm $NS sh -c "tcpdump -n -l -i any udp port 53 > /tmp/c.txt 2>&1; sleep 600" >/dev/null; sleep 2
restart_timed | tee -a "$OUT/capture.txt"; sleep 3
docker exec phosphor-tele-cap sh -c 'pkill tcpdump; sleep 1; cat /tmp/c.txt' > "$OUT/caseC-tcpdump.txt"; docker rm -f phosphor-tele-cap >/dev/null
echo "  packets on any interface: $(grep -cE '^[0-9]{2}:' "$OUT/caseC-tcpdump.txt")" | tee -a "$OUT/capture.txt"
grep -E '^[0-9]{2}:' "$OUT/caseC-tcpdump.txt" | cut -c1-200 | sed 's/^/  /' | tee -a "$OUT/capture.txt"
docker exec phosphor-tele-fpm sh -c 'getent hosts telemetry.sury.org || echo "  getent: telemetry.sury.org does not resolve"' | tee -a "$OUT/capture.txt"
