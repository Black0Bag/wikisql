#!/bin/bash
# wikisql 路由器部署脚本
# 用法: SSHPASS='路由器root密码' bash scripts/deploy-router.sh <二进制路径> [路由器地址]
# 说明: 停服 → 上传二进制 → 确保 admin 配置段 → 启动 → 显示初始管理密码
set -e
BINARY="${1:-/tmp/wikisql-arm64}"
ROUTER_ADDR="${2:-192.168.1.2}"
if [ -z "$SSHPASS" ]; then
  echo "请先设置 SSHPASS 环境变量（路由器 root 密码）"; exit 1
fi
ROUTER="root@$ROUTER_ADDR"

echo "[1/5] 停服务"
sshpass -e ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 $ROUTER '/etc/init.d/wikisql stop'

echo "[2/5] 上传新二进制"
sshpass -e scp -o StrictHostKeyChecking=no "$BINARY" $ROUTER:/root/wikisql/wikisql

echo "[3/5] 检查/补充 admin 配置段"
sshpass -e ssh -o StrictHostKeyChecking=no $ROUTER '
if ! grep -q "^admin:" /root/wikisql/config.yaml; then
  sed -i "/^files:/i admin:\n  enabled: true\n  password_hash: \"\"\n  session_ttl_hours: 24\n" /root/wikisql/config.yaml
fi
chmod +x /root/wikisql/wikisql
grep -A 3 "^admin:" /root/wikisql/config.yaml'

echo "[4/5] 启动服务"
sshpass -e ssh -o StrictHostKeyChecking=no $ROUTER '/etc/init.d/wikisql start; sleep 6; netstat -tln | grep 8210'

echo "[5/5] 读取初始管理密码（如未生成过）"
sshpass -e ssh -o StrictHostKeyChecking=no $ROUTER 'grep "管理面板初始密码" /tmp/wikisql.log | tail -1 || true'
echo "完成"
