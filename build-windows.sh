#!/bin/bash

echo "Building News Aggregator for Windows (64-bit)..."
GOOS=windows GOARCH=amd64 go build -o news-aggregator.exe ./cmd/server

if [ $? -eq 0 ]; then
    echo "Build successful! Executable: news-aggregator.exe"
    echo ""
    echo "To deploy on Windows:"
    echo "1. Copy news-aggregator.exe to your Windows machine"
    echo "2. Copy the 'web' directory (contains templates)"
    echo "3. Copy the 'config' directory (or create it and add config.json)"
    echo "4. Place credentials.json in the same directory as the executable"
    echo "5. Run: news-aggregator.exe"
else
    echo "Build failed!"
    exit 1
fi
