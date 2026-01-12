# Storage Comparison

| Feature | Local Storage | S3 Storage |
| --- | --- | --- |
| **Setup Complexity** | Simple | Moderate |
| **External Dependencies** | None | S3 service required |
| **Performance** | Fast (local I/O) | Network latency |
| **Scalability** | Limited to disk | Unlimited |
| **High Availability** | ❌ Not supported | ✅ Required |
| **Redundancy** | None (unless RAID/NFS) | Built-in |
| **Cost** | Disk only | S3 storage + requests |
| **Best For** | Single-instance, dev/test | HA, production, cloud |
