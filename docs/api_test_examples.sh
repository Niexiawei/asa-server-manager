#!/bin/bash

# ASA Server Manager API Test Examples
# This script demonstrates how to use the HTTP API endpoints

BASE_URL="http://localhost:8080"

echo "=========================================="
echo "ASA Server Manager API Test Examples"
echo "=========================================="
echo ""

# Health Check
echo "1. Health Check"
echo "curl $BASE_URL/health"
curl -s "$BASE_URL/health" | jq .
echo ""

# List all instances
echo "2. List All Instances"
echo "curl $BASE_URL/api/instances"
curl -s "$BASE_URL/api/instances" | jq .
echo ""

# Create a new instance
echo "3. Create New Instance"
echo "curl -X POST $BASE_URL/api/instances \\"
echo "  -H 'Content-Type: application/json' \\"
echo "  -d '{\"name\":\"test-instance\"}'"
curl -s -X POST "$BASE_URL/api/instances" \
  -H "Content-Type: application/json" \
  -d '{"name":"test-instance"}' | jq .
echo ""

# Get instance status
echo "4. Get Instance Status"
echo "curl $BASE_URL/api/instances/test-instance"
curl -s "$BASE_URL/api/instances/test-instance" | jq .
echo ""

# Start server
echo "5. Start Server"
echo "curl -X POST $BASE_URL/api/server/test-instance/start"
curl -s -X POST "$BASE_URL/api/server/test-instance/start" | jq .
echo ""

# Check status again
echo "6. Check Status After Start"
echo "curl $BASE_URL/api/instances/test-instance"
curl -s "$BASE_URL/api/instances/test-instance" | jq .
echo ""

# Send RCON command
echo "7. Send RCON Command"
echo "curl -X POST $BASE_URL/api/rcon/test-instance/command \\"
echo "  -H 'Content-Type: application/json' \\"
echo "  -d '{\"command\":\"ListPlayers\"}'"
curl -s -X POST "$BASE_URL/api/rcon/test-instance/command" \
  -H "Content-Type: application/json" \
  -d '{"command":"ListPlayers"}' | jq .
echo ""

# Create backup
echo "8. Create Backup"
echo "curl -X POST $BASE_URL/api/backup/test-instance \\"
echo "  -H 'Content-Type: application/json' \\"
echo "  -d '{\"world_folder\":\"TheIsland_WP\"}'"
curl -s -X POST "$BASE_URL/api/backup/test-instance" \
  -H "Content-Type: application/json" \
  -d '{"world_folder":"TheIsland_WP"}' | jq .
echo ""

# List backups
echo "9. List Backups"
echo "curl $BASE_URL/api/backup"
curl -s "$BASE_URL/api/backup" | jq .
echo ""

# Restart server
echo "10. Restart Server"
echo "curl -X POST $BASE_URL/api/server/test-instance/restart"
curl -s -X POST "$BASE_URL/api/server/test-instance/restart" | jq .
echo ""

# Stop server
echo "11. Stop Server"
echo "curl -X POST $BASE_URL/api/server/test-instance/stop"
curl -s -X POST "$BASE_URL/api/server/test-instance/stop" | jq .
echo ""

# Rename instance
echo "12. Rename Instance"
echo "curl -X PUT $BASE_URL/api/instances/test-instance \\"
echo "  -H 'Content-Type: application/json' \\"
echo "  -d '{\"new_name\":\"renamed-instance\"}'"
curl -s -X PUT "$BASE_URL/api/instances/test-instance" \
  -H "Content-Type: application/json" \
  -d '{"new_name":"renamed-instance"}' | jq .
echo ""

# Delete instance
echo "13. Delete Instance"
echo "curl -X DELETE $BASE_URL/api/instances/renamed-instance"
curl -s -X DELETE "$BASE_URL/api/instances/renamed-instance" | jq .
echo ""

# Start all servers
echo "14. Start All Servers"
echo "curl -X POST $BASE_URL/api/server/start-all"
curl -s -X POST "$BASE_URL/api/server/start-all" | jq .
echo ""

# Stop all servers
echo "15. Stop All Servers"
echo "curl -X POST $BASE_URL/api/server/stop-all"
curl -s -X POST "$BASE_URL/api/server/stop-all" | jq .
echo ""

# Update server
echo "16. Update Server"
echo "curl -X POST '$BASE_URL/api/server/update?force-server=true'"
curl -s -X POST "$BASE_URL/api/server/update?force-server=true" | jq .
echo ""

echo "=========================================="
echo "All tests completed!"
echo "=========================================="
