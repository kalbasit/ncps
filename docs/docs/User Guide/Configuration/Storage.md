# Storage
## Storage Configuration

Configure ncps storage backends: local filesystem or S3-compatible storage.

## Overview

ncps supports two storage backends for storing NAR files and other cache data:

*   **Local Filesystem**: Traditional file-based storage
*   **S3-Compatible**: AWS S3, MinIO, and other S3-compatible services

**Note:** You must choose exactly ONE storage backend. You cannot use both simultaneously.

## Next Steps

1.  <a class="reference-link" href="Database.md">Database</a> - Configure database backend
2.  <a class="reference-link" href="Reference.md">Reference</a> - All storage options
3.  <a class="reference-link" href="../Deployment/High%20Availability.md">High Availability</a> - S3 for HA deployments
4.  <a class="reference-link" href="../Operations.md">Operations</a> - Monitoring and maintenance

## Related Documentation

*   <a class="reference-link" href="Reference.md">Reference</a> - All configuration options
*   <a class="reference-link" href="../Installation.md">Installation</a> - Installation-specific storage setup
*   <a class="reference-link" href="../../Developer%20Guide/Architecture/Storage%20Backends/S3%20Storage%20Implementation.md">S3 Storage Implementation</a> - S3 implementation details