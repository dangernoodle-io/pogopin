#!/bin/bash
# Inject context when working in an ESP-IDF project
if [ -f "$PWD/sdkconfig" ] || grep -q "idf_component_register" "$PWD/CMakeLists.txt" 2>/dev/null; then
    echo "[pogopin] ESP-IDF project detected — use esp_info to identify chip before flashing"
fi
