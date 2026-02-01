# Troubleshooting

### Local Storage Issues

**Permission Denied:**

```
# Check ownership
ls -la /var/lib/ncps

# Fix ownership
sudo chown -R ncps:ncps /var/lib/ncps
```

**Disk Full:**

```
# Check disk usage
df -h /var/lib/ncps

# Configure LRU cleanup
ncps serve --cache-max-size=50G --cache-lru-schedule="0 2 * * *"
```

### S3 Storage Issues

**Access Denied:**

- Verify credentials are correct
- Check IAM policy permissions
- Ensure bucket exists and is in correct region

**Connection Timeout:**

- Check network connectivity to S3 endpoint
- Verify endpoint URL is correct
- Check firewall rules

**Slow Performance:**

- Check network bandwidth
- Consider using S3 Transfer Acceleration (AWS)
- Verify region is geographically close

See the <a class="reference-link" href="../../Operations/Troubleshooting.md">Troubleshooting</a> for more help.
