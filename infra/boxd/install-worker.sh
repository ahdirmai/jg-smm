#!/bin/sh
# One-time worker build for boxd. Run on smm-worker-golden before snapshoting.
# The sealed .boxenv.base (copied by the operator, mode 600) is per-fleet and
# must exist before this runs.
set -eu
cd "$(dirname "$0")/../.."
repo=$(pwd)

sudo apt-get update -qq
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
  xvfb x11vnc novnc websockify fonts-noto-color-emoji fonts-liberation nginx
sudo npm i -g --prefix /usr/local pnpm@9.12.0
/usr/local/bin/pnpm install --frozen-lockfile
/usr/local/bin/pnpm --filter @smm/shared build
/usr/local/bin/pnpm --filter @smm/worker build
/usr/local/bin/pnpm --filter @smm/worker exec playwright install --with-deps chromium

chmod 755 "$repo/infra/boxd/render-worker-env.sh"
sudo install -m 644 "$repo/infra/boxd/nginx-worker.conf" /etc/nginx/sites-available/smm-worker
sudo ln -sf /etc/nginx/sites-available/smm-worker /etc/nginx/sites-enabled/smm-worker
sudo rm -f /etc/nginx/sites-enabled/default
sudo nginx -t
sudo systemctl enable --now nginx
sudo install -m 644 "$repo"/infra/boxd/systemd/*.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now smm-xvfb smm-x11vnc smm-novnc smm-worker
sudo systemctl restart smm-worker
echo "worker installed: $(hostname)"
