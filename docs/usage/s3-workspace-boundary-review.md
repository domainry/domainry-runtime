# S3 workspace boundary review

The S3 adapter rejects an empty Workspace in Stage (line-content hash 8afd515a98f72b3c), Commit (d03170ca4dd1a4a4) and object-key resolution (d062ed0852dd26b8). These branches return an error before any object-store operation. None selects a default Workspace. Object keys derive their prefix from the supplied nonempty Workspace; the existing S3 test confirms a blob in workspace-a is absent from workspace-b.

The three exact source fingerprints were missing from the v0.1.268 release baseline. They existed only in a local review file, which made a working-tree test insufficient to prove the published package. This patch records the individually reviewed guards; it neither broadens the inventory matcher nor changes S3 behavior. The published-package boundary and full tests are required for acceptance.
