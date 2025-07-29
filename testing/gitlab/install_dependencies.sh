#!/bin/bash

# Install Python dependencies for upgrade testing
# This script installs the required Python packages for running upgrade tests

set -euo pipefail

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Installing Python dependencies for upgrade testing..."

# Check if Python is available
if ! command -v python3 &> /dev/null; then
    echo "Error: python3 is not installed or not in PATH"
    exit 1
fi

# Check if pip is available
if ! command -v pip3 &> /dev/null; then
    echo "Error: pip3 is not installed or not in PATH"
    exit 1
fi

# Upgrade pip to latest version
echo "Upgrading pip..."
python3 -m pip install --upgrade pip

# Install dependencies from requirements.txt
echo "Installing dependencies from requirements.txt..."
pip3 install -r "${SCRIPT_DIR}/requirements.txt"

echo "Dependencies installed successfully!"
echo ""
echo "To run the tests, use:"
echo "  cd ${SCRIPT_DIR}"
echo "  pytest upgrade_gitlab_test.py -v"
echo ""
echo "To run tests in order:"
echo "  pytest upgrade_gitlab_test.py -v --order-dependencies" 
