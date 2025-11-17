#!/bin/bash

# ASA Server Manager - Go 版本 使用示例
# 这个脚本演示了如何使用 asa-manager 工具的常见操作

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

MANAGER="./asa-server"

# 示例 0: 更新/安装服务器
echo -e "${YELLOW}示例 0: 更新/安装服务器${NC}"
echo "命令: $MANAGER update"
echo "（下载并解压 SteamCMD，用于安装/更新服务器文件）"
echo ""

# ... existing code ...
echo -e "\n${YELLOW}示例 1: 列出所有实例${NC}"
echo "命令: $MANAGER list"
# $MANAGER list

# 示例 2: 创建新实例（交互式）
echo -e "\n${YELLOW}示例 2: 创建新实例${NC}"
echo "命令: $MANAGER create"
echo "（需要交互式输入实例名称）"

# 示例 3: 启动特定实例
echo -e "\n${YELLOW}示例 3: 启动实例${NC}"
echo "命令: $MANAGER start server1"
# $MANAGER start server1

# 示例 4: 停止实例
echo -e "\n${YELLOW}示例 4: 停止实例${NC}"
echo "命令: $MANAGER stop server1"
# $MANAGER stop server1

# 示例 5: 重启实例
echo -e "\n${YELLOW}示例 5: 重启实例${NC}"
echo "命令: $MANAGER restart server1"
# $MANAGER restart server1

# 示例 6: 检查所有实例状态
echo -e "\n${YELLOW}示例 6: 检查所有实例状态${NC}"
echo "命令: $MANAGER status"
# $MANAGER status

# 示例 7: 检查特定实例状态
echo -e "\n${YELLOW}示例 7: 检查特定实例状态${NC}"
echo "命令: $MANAGER status server1"
# $MANAGER status server1

# 示例 8: 创建备份
echo -e "\n${YELLOW}示例 8: 创建备份${NC}"
echo "命令: $MANAGER backup server1 TheIsland_WP"
# $MANAGER backup server1 TheIsland_WP

# 示例 9: 恢复备份
echo -e "\n${YELLOW}示例 9: 恢复备份${NC}"
echo "命令: $MANAGER restore server1"
echo "（需要交互式选择备份）"
# $MANAGER restore server1

# 示例 10: 发送 RCON 命令
echo -e "\n${YELLOW}示例 10: 发送 RCON 命令${NC}"
echo "命令: $MANAGER rcon server1 \"SaveWorld\""
# $MANAGER rcon server1 "SaveWorld"

# 示例 11: 重命名实例
echo -e "\n${YELLOW}示例 11: 重命名实例${NC}"
echo "命令: $MANAGER rename server1"
echo "（需要交互式输入新名称）"
# $MANAGER rename server1

# 示例 12: 删除实例
echo -e "\n${YELLOW}示例 12: 删除实例${NC}"
echo "命令: $MANAGER delete server1"
echo "（需要确认删除）"
# $MANAGER delete server1

# 示例 13: 启动所有实例
echo -e "\n${YELLOW}示例 13: 启动所有实例${NC}"
echo "命令: $MANAGER start-all"
# $MANAGER start-all

# 示例 14: 停止所有实例
echo -e "\n${YELLOW}示例 14: 停止所有实例${NC}"
echo "命令: $MANAGER stop-all"
# $MANAGER stop-all

# 示例 15: 交互式管理实例
echo -e "\n${YELLOW}示例 15: 交互式管理实例${NC}"
echo "命令: $MANAGER manage server1"
echo "（进入交互式菜单，支持多个操作）"
# $MANAGER manage server1

# 示例 16: 获取帮助
echo -e "\n${YELLOW}示例 16: 获取帮助${NC}"
echo "命令: $MANAGER --help"
# $MANAGER --help

echo -e "\n${YELLOW}示例 17: 获取特定命令的帮助${NC}"
echo "命令: $MANAGER start --help"
# $MANAGER start --help

echo -e "\n${BLUE}========================================${NC}"
echo -e "${BLUE}更多信息请查看 README.md${NC}"
echo -e "${BLUE}========================================${NC}"
