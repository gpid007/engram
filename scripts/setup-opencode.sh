#!/bin/bash
# Setup Engram as an MCP server in OpenCode
# Usage: ./scripts/setup-opencode.sh [config-path]

set -e

CONFIG_PATH="${1:-$HOME/.config/opencode/opencode.json}"
ENGRAM_BIN="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/bin/engram"
ENGRAM_CONFIG="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/engram.local.yaml"

echo "🔧 Setting up Engram as OpenCode MCP server..."
echo ""

# Check if Engram binary exists
if [ ! -f "$ENGRAM_BIN" ]; then
    echo "❌ Engram binary not found at $ENGRAM_BIN"
    echo "   Build it first: go build -o ./bin/engram ./cmd/engram"
    exit 1
fi

# Check if config directory exists
if [ ! -d "$(dirname "$CONFIG_PATH")" ]; then
    echo "📁 Creating OpenCode config directory..."
    mkdir -p "$(dirname "$CONFIG_PATH")"
fi

# Backup existing config if it exists
if [ -f "$CONFIG_PATH" ]; then
    echo "💾 Backing up existing config to ${CONFIG_PATH}.bak"
    cp "$CONFIG_PATH" "${CONFIG_PATH}.bak"
fi

# Create or update config
echo "📝 Updating OpenCode config..."

# Check if config is JSON or JSONC (look for existing mcp section)
if [ -f "$CONFIG_PATH" ] && grep -q '"mcp"' "$CONFIG_PATH"; then
    # File exists and has mcp section - use jq to update
    if command -v jq &> /dev/null; then
        jq '.mcp.engram = {
          "type": "local",
          "command": ["'"$ENGRAM_BIN"'", "-config", "'"$ENGRAM_CONFIG"'"],
          "enabled": true,
          "timeout": 10000
        }' "$CONFIG_PATH" > "${CONFIG_PATH}.tmp" && mv "${CONFIG_PATH}.tmp" "$CONFIG_PATH"
    else
        echo "⚠️  jq not found, creating new config file"
        cat > "$CONFIG_PATH" << EOF
{
  "\$schema": "https://opencode.ai/config.json",
  "mcp": {
    "engram": {
      "type": "local",
      "command": ["$ENGRAM_BIN", "-config", "$ENGRAM_CONFIG"],
      "enabled": true,
      "timeout": 10000
    }
  }
}
EOF
    fi
else
    # Create new config
    cat > "$CONFIG_PATH" << EOF
{
  "\$schema": "https://opencode.ai/config.json",
  "mcp": {
    "engram": {
      "type": "local",
      "command": ["$ENGRAM_BIN", "-config", "$ENGRAM_CONFIG"],
      "enabled": true,
      "timeout": 10000
    }
  }
}
EOF
fi

echo "✅ Configuration saved to: $CONFIG_PATH"
echo ""
echo "📋 Configuration:"
echo "  Binary: $ENGRAM_BIN"
echo "  Config: $ENGRAM_CONFIG"
echo "  Timeout: 10000ms"
echo ""
echo "🚀 Next steps:"
echo "  1. Build Engram: go build -o ./bin/engram ./cmd/engram"
echo "  2. Start Engram: ./run-local.sh"
echo "  3. Start OpenCode: opencode"
echo "  4. Check MCP status: /mcp engram"
echo "  5. Use Engram tools in your prompts: 'use engram'"
echo ""
echo "📚 Documentation: docs/OPENCODE_INTEGRATION.md"
