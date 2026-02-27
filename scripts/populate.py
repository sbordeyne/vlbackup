# Script to populate a VictoriaLogs instance with sample logs for testing purposes
# Using the loki push API cause OpenTelemetry is overkill for 100 log lines nobody cares about

import requests
import time

logs = [
    f"This is a test log line {i}"
    for i in range(100)
]

data = {
  "streams": [
    {
      "stream": {
        "env": "test"
      },
      "values": [
          [ str(time.time_ns()), log_line ]
          for log_line in logs
      ]
    }
  ]
}

response = requests.post("http://localhost:9428/insert/loki/api/v1/push", json=data)
print(response.status_code)
