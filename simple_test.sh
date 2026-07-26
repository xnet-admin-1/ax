echo "=== AX API TEST RESULTS ==="

echo -e "\n1. Testing Tool Execution API:"
curl -v -X POST http://localhost:8080/api/v1/tools/execute \
  -H "Authorization: Bearer dev-key-123" \
  -H "Content-Type: application/json" \
  -d '{"tool":"run_sh","parameters":{"command":"echo AX_TEST && pwd"},"trust":true}'

echo -e "\n2. Testing Agent Orchestration API:"
curl -v -X POST http://localhost:8080/api/v1/agents/spawn \
  -H "Authorization: Bearer ci-key-456" \
  -H "Content-Type: application/json" \
  -d '{"agents":["coder"],"task":"Write hello world","trust":true}'

echo -e "\n3. Testing Authentication:"
curl -v -X POST http://localhost:8080/api/v1/tools/execute \
  -H "Authorization: Bearer wrong-key" \
  -H "Content-Type: application/json" \
  -d '{"tool":"run_sh","parameters":{"command":"echo test"}}'

echo -e "\n4. Testing MCP Integration:"
curl -v -X POST http://localhost:8080/api/v1/mcp/filesystem \
  -H "Authorization: Bearer ci-key-456" \
  -H "Content-Type: application/json" \
  -d '{"operation":"list","path":"/tmp","trust":true}'

echo -e "\n=== TESTING COMPLETE ==="
