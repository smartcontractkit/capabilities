ps aux | grep "integration_tests_temp" | grep -v grep | awk '{print $2}' | xargs kill -9
