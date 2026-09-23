# EventStreams

EventStreams provision Kafka clusters and topics. They are **namespace-scoped**.

```yaml
apiVersion: shoulders.io/v1alpha1
kind: EventStream
metadata:
  name: team-a-01
  namespace: team-a
spec:
  topics:
    - name: logs
    - name: events
      partitions: 5
      config:
        retention.ms: "604800000"
```

This provisions:

- A Strimzi **KafkaNodePool** (`<name>-pool`) with 3 broker+controller nodes.
- A Strimzi **Kafka** cluster (`<name>-cluster`) in KRaft mode with plain and TLS listeners.
- A **KafkaTopic** per `topics[]` entry (`partitions` default 3, `replicas` default 3).

CLI:

```bash
shoulders infra add-stream <name>  # --topics, --partitions, --replicas, --topic-config
```
