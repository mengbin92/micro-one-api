-- Display metadata never changes code-owned execution eligibility or scope.
ALTER TABLE iam_permissions ADD COLUMN description VARCHAR(8192) NOT NULL DEFAULT '';
ALTER TABLE iam_permissions ADD COLUMN sort INTEGER NOT NULL DEFAULT 0;
