# Migration Between Storage Backends

### From Local to S3

```
# 1. Sync data to S3
aws s3 sync /var/lib/ncps/nar s3://ncps-cache/nar/
aws s3 sync /var/lib/ncps/narinfo s3://ncps-cache/narinfo/
aws s3 sync /var/lib/ncps/config s3://ncps-cache/config/

# 2. Update ncps configuration to use S3
# 3. Restart ncps

# 4. Verify and clean up local storage (optional)
rm -rf /var/lib/ncps/nar /var/lib/ncps/narinfo
```

### From S3 to Local

```
# 1. Sync data from S3
aws s3 sync s3://ncps-cache/nar/ /var/lib/ncps/nar/
aws s3 sync s3://ncps-cache/narinfo/ /var/lib/ncps/narinfo/
aws s3 sync s3://ncps-cache/config/ /var/lib/ncps/config/

# 2. Update ncps configuration to use local storage
# 3. Restart ncps
```
