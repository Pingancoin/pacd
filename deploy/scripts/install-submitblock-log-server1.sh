#!/usr/bin/env bash
set -euo pipefail

rm -rf /tmp/pacd-deploy
mkdir -p /tmp/pacd-deploy
tar -xzf /tmp/pacd-deploy.tgz -C /tmp/pacd-deploy
sha256sum /tmp/pacd-deploy/pacd

install -o root -g root -m 0755 /usr/local/bin/pacd /usr/local/bin/pacd.backup.submitblock
install -o root -g root -m 0755 /tmp/pacd-deploy/pacd /usr/local/bin/pacd
install -d -o pacd -g pacd -m 0755 /var/log/pingancoin
install -o root -g root -m 0644 /tmp/pacd-deploy/pacd-submitblock /etc/logrotate.d/pacd-submitblock

if ! grep -q '^PACD_SUBMITBLOCK_LOG=' /etc/pingancoin/pacd-mainnet.env; then
  printf '\nPACD_SUBMITBLOCK_LOG=/var/log/pingancoin/submitblock.jsonl\n' >> /etc/pingancoin/pacd-mainnet.env
fi

cat > /etc/systemd/system/pacd-mainnet.service.d/zz-submitblock-log.conf <<'DROPIN'
[Service]
ReadWritePaths=/var/log/pingancoin
ExecStart=
ExecStart=/usr/local/bin/pacd --network ${PACD_NETWORK} --datadir ${PACD_DATADIR} --rpc --rpclisten ${PACD_RPC_LISTEN} --rpctoken=${PACD_RPC_TOKEN} --submitblocklog=${PACD_SUBMITBLOCK_LOG} --p2p --listen ${PACD_P2P_LISTEN} --maxpeers ${PACD_MAX_PEERS} --stratum=${PACD_STRATUM} --stratumlisten=${PACD_STRATUM_LISTEN} --stratumaddress=${PACD_STRATUM_ADDRESS} --stratumdiff=${PACD_STRATUM_DIFF} --stratumrefresh=${PACD_STRATUM_REFRESH} --connect server2.pingancoin.org:9508 --connect zsses.com:9508
DROPIN

systemctl daemon-reload
systemctl restart pacd-mainnet
systemctl is-active pacd-mainnet
