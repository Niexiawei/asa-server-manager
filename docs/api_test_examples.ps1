# ASA Server Manager API Test Examples
# PowerShell script for testing HTTP API endpoints

$BaseUrl = "http://localhost:8080"

function Make-Request {
    param(
        [string]$Method,
        [string]$Endpoint,
        [object]$Body = $null
    )
    
    $Uri = "$BaseUrl$Endpoint"
    $Headers = @{"Content-Type" = "application/json"}
    
    if ($Body) {
        $BodyJson = $Body | ConvertTo-Json
        $response = Invoke-RestMethod -Uri $Uri -Method $Method -Headers $Headers -Body $BodyJson
    } else {
        $response = Invoke-RestMethod -Uri $Uri -Method $Method -Headers $Headers
    }
    
    return $response | ConvertTo-Json -Depth 10
}

Write-Host "==========================================" -ForegroundColor Cyan
Write-Host "ASA Server Manager API Test Examples" -ForegroundColor Cyan
Write-Host "==========================================" -ForegroundColor Cyan
Write-Host ""

# Health Check
Write-Host "1. Health Check" -ForegroundColor Yellow
Write-Host "GET /health" -ForegroundColor Gray
Write-Host (Make-Request -Method "Get" -Endpoint "/health")
Write-Host ""

# List all instances
Write-Host "2. List All Instances" -ForegroundColor Yellow
Write-Host "GET /api/instances" -ForegroundColor Gray
Write-Host (Make-Request -Method "Get" -Endpoint "/api/instances")
Write-Host ""

# Create a new instance
Write-Host "3. Create New Instance" -ForegroundColor Yellow
Write-Host "POST /api/instances" -ForegroundColor Gray
$body = @{name = "test-instance"}
Write-Host (Make-Request -Method "Post" -Endpoint "/api/instances" -Body $body)
Write-Host ""

# Get instance status
Write-Host "4. Get Instance Status" -ForegroundColor Yellow
Write-Host "GET /api/instances/test-instance" -ForegroundColor Gray
Write-Host (Make-Request -Method "Get" -Endpoint "/api/instances/test-instance")
Write-Host ""

# Start server
Write-Host "5. Start Server" -ForegroundColor Yellow
Write-Host "POST /api/server/test-instance/start" -ForegroundColor Gray
Write-Host (Make-Request -Method "Post" -Endpoint "/api/server/test-instance/start")
Write-Host ""

# Check status again
Write-Host "6. Check Status After Start" -ForegroundColor Yellow
Write-Host "GET /api/instances/test-instance" -ForegroundColor Gray
Write-Host (Make-Request -Method "Get" -Endpoint "/api/instances/test-instance")
Write-Host ""

# Send RCON command
Write-Host "7. Send RCON Command" -ForegroundColor Yellow
Write-Host "POST /api/rcon/test-instance/command" -ForegroundColor Gray
$body = @{command = "ListPlayers"}
Write-Host (Make-Request -Method "Post" -Endpoint "/api/rcon/test-instance/command" -Body $body)
Write-Host ""

# Create backup
Write-Host "8. Create Backup" -ForegroundColor Yellow
Write-Host "POST /api/backup/test-instance" -ForegroundColor Gray
$body = @{world_folder = "TheIsland_WP"}
Write-Host (Make-Request -Method "Post" -Endpoint "/api/backup/test-instance" -Body $body)
Write-Host ""

# List backups
Write-Host "9. List Backups" -ForegroundColor Yellow
Write-Host "GET /api/backup" -ForegroundColor Gray
Write-Host (Make-Request -Method "Get" -Endpoint "/api/backup")
Write-Host ""

# Restart server
Write-Host "10. Restart Server" -ForegroundColor Yellow
Write-Host "POST /api/server/test-instance/restart" -ForegroundColor Gray
Write-Host (Make-Request -Method "Post" -Endpoint "/api/server/test-instance/restart")
Write-Host ""

# Stop server
Write-Host "11. Stop Server" -ForegroundColor Yellow
Write-Host "POST /api/server/test-instance/stop" -ForegroundColor Gray
Write-Host (Make-Request -Method "Post" -Endpoint "/api/server/test-instance/stop")
Write-Host ""

# Rename instance
Write-Host "12. Rename Instance" -ForegroundColor Yellow
Write-Host "PUT /api/instances/test-instance" -ForegroundColor Gray
$body = @{new_name = "renamed-instance"}
Write-Host (Make-Request -Method "Put" -Endpoint "/api/instances/test-instance" -Body $body)
Write-Host ""

# Delete instance
Write-Host "13. Delete Instance" -ForegroundColor Yellow
Write-Host "DELETE /api/instances/renamed-instance" -ForegroundColor Gray
Write-Host (Make-Request -Method "Delete" -Endpoint "/api/instances/renamed-instance")
Write-Host ""

# Start all servers
Write-Host "14. Start All Servers" -ForegroundColor Yellow
Write-Host "POST /api/server/start-all" -ForegroundColor Gray
Write-Host (Make-Request -Method "Post" -Endpoint "/api/server/start-all")
Write-Host ""

# Stop all servers
Write-Host "15. Stop All Servers" -ForegroundColor Yellow
Write-Host "POST /api/server/stop-all" -ForegroundColor Gray
Write-Host (Make-Request -Method "Post" -Endpoint "/api/server/stop-all")
Write-Host ""

# Update server
Write-Host "16. Update Server" -ForegroundColor Yellow
Write-Host "POST /api/server/update?force-server=true" -ForegroundColor Gray
Write-Host (Make-Request -Method "Post" -Endpoint "/api/server/update?force-server=true")
Write-Host ""

Write-Host "==========================================" -ForegroundColor Cyan
Write-Host "All tests completed!" -ForegroundColor Cyan
Write-Host "==========================================" -ForegroundColor Cyan
