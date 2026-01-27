#!/bin/bash

echo "Building News Aggregator for Windows (32-bit)..."
GOOS=windows GOARCH=386 go build -o news-aggregator-32.exe ./cmd/server

if [ $? -eq 0 ]; then
    echo "Build successful! Executable: news-aggregator-32.exe"
    echo ""
    echo "To deploy on Windows:"
    echo "1. Copy news-aggregator-32.exe to your Windows machine"
    echo "2. Copy the 'web' directory (contains templates)"
    echo "3. Copy the 'config' directory (or create it and add config.json)"
    echo "4. Place credentials.json in the same directory as the executable"
    echo "5. Run: news-aggregator-32.exe"
else
    echo "Build failed!"
    exit 1
fi
