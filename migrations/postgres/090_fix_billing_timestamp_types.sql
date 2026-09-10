-- Postgres baseline 046 parity: preserve existing account receivables while
-- changing integer Unix-second timestamps to TIMESTAMPTZ. The text conversion
-- also accepts a fresh baseline that already declares TIMESTAMPTZ.


ALTER TABLE account_receivables ALTER COLUMN created_at DROP DEFAULT;
ALTER TABLE account_receivables ALTER COLUMN created_at TYPE TIMESTAMPTZ USING (
  CASE
    WHEN created_at IS NULL OR created_at::text = '0' THEN NULL
    WHEN created_at::text ~ '^-?[0-9]+([.][0-9]+)?$' THEN to_timestamp(created_at::text::double precision)
    ELSE created_at::text::timestamptz
  END
);

ALTER TABLE account_receivables ALTER COLUMN updated_at DROP DEFAULT;
ALTER TABLE account_receivables ALTER COLUMN updated_at TYPE TIMESTAMPTZ USING (
  CASE
    WHEN updated_at IS NULL OR updated_at::text = '0' THEN NULL
    WHEN updated_at::text ~ '^-?[0-9]+([.][0-9]+)?$' THEN to_timestamp(updated_at::text::double precision)
    ELSE updated_at::text::timestamptz
  END
);

ALTER TABLE account_receivables ALTER COLUMN settled_at DROP DEFAULT;
ALTER TABLE account_receivables ALTER COLUMN settled_at TYPE TIMESTAMPTZ USING (
  CASE
    WHEN settled_at IS NULL OR settled_at::text = '0' THEN NULL
    WHEN settled_at::text ~ '^-?[0-9]+([.][0-9]+)?$' THEN to_timestamp(settled_at::text::double precision)
    ELSE settled_at::text::timestamptz
  END
);
