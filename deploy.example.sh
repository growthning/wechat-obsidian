#!/bin/bash
set -e

# Config — copy this file to deploy.sh and fill in your values
VPS_HOST="user@your-server-ip"
SSH_KEY="$HOME/.ssh/id_rsa"
REMOTE_DIR="/home/user/wechat-obsidian"
PLUGIN_DIR="$HOME/your-vault/.obsidian/plugins/wechat-sync"
SSH="ssh -i $SSH_KEY $VPS_HOST"
SCP="scp -i $SSH_KEY"

# Flags
RESET=false
for arg in "$@"; do
  case $arg in
    --reset) RESET=true ;;
  esac
done

echo "=== 1. Build server ==="
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o wechat-obsidian-server ./cmd/server/
echo "OK"

echo "=== 2. Build plugin ==="
(cd obsidian-plugin && npm run build --silent)
echo "OK"

echo "=== 3. Deploy server ==="
$SSH "sudo systemctl stop wechat-obsidian"
$SCP wechat-obsidian-server "$VPS_HOST:$REMOTE_DIR/server"

if $RESET; then
  echo "=== 3a. Reset server data ==="
  $SSH "rm -f $REMOTE_DIR/data/wechat.db* && rm -rf $REMOTE_DIR/data/images/"
  echo "OK"
fi

$SSH "sudo systemctl start wechat-obsidian"
echo "OK"

echo "=== 4. Verify server ==="
sleep 2
STATUS=$($SSH "sudo systemctl is-active wechat-obsidian")
if [ "$STATUS" != "active" ]; then
  echo "FAIL: server is $STATUS"
  $SSH "sudo journalctl -u wechat-obsidian --no-pager -n 10"
  exit 1
fi
echo "OK (active)"

echo "=== 5. Deploy plugin ==="
cp obsidian-plugin/main.js "$PLUGIN_DIR/main.js"
echo "OK"

echo ""
echo "=== Done ==="
echo "Usage: ./deploy.sh          # deploy only"
echo "       ./deploy.sh --reset  # deploy + clear all data"
