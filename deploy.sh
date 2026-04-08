#!/bin/bash
set -e

# Config
VPS_HOST="growthning@35.212.133.212"
SSH_KEY="/Users/liuning/.ssh/id_rsa"
REMOTE_DIR="/home/growthning/wechat-obsidian"
PLUGIN_DIR="/Users/liuning/nico_notes/.obsidian/plugins/wechat-sync"
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
# Verify binary is new by checking health endpoint
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://35.212.133.212:8900/health")
if [ "$HTTP_CODE" != "200" ]; then
  echo "FAIL: health check returned $HTTP_CODE"
  exit 1
fi
echo "OK (active, health=200)"

echo "=== 5. Deploy plugin ==="
cp obsidian-plugin/main.js "$PLUGIN_DIR/main.js"
echo "OK"

if $RESET; then
  echo "=== 5a. Reset local data ==="
  rm -rf /Users/liuning/nico_notes/articles /Users/liuning/nico_notes/daily /Users/liuning/nico_notes/attachments 2>/dev/null || true
  cat > "$PLUGIN_DIR/data.json" << 'CONF'
{
  "serverUrl": "http://35.212.133.212:8900",
  "apiKey": "wechat-obsidian-sync-2026",
  "pollInterval": 10,
  "dailyFolder": "daily",
  "articlesFolder": "articles",
  "attachmentsFolder": "attachments",
  "lastSyncedId": 0
}
CONF
  echo "OK (restart Obsidian plugin to apply)"
fi

echo ""
echo "=== Done ==="
echo "Usage: ./deploy.sh          # deploy only"
echo "       ./deploy.sh --reset  # deploy + clear all data"
