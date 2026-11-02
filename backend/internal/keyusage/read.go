package keyusage

// Full covered buckets, pending signed changes and raw gaps are read in one
// statement snapshot. The raw branch uses disjoint time ranges, not a per-row
// anti-join over the complete history. Today's open-ended upper bound preserves
// the existing API's semantics, including future-dated stored rows.
const readSQL = `
WITH active AS MATERIALIZED (
 SELECT COALESCE((SELECT (value::jsonb->>'enabled')::boolean FROM settings WHERE key=$5),false) AS enabled
), ranges AS (
 SELECT 'total'::text AS kind,$2::timestamptz AS lo,$3::timestamptz AS hi FROM active WHERE enabled AND $3::timestamptz>$2::timestamptz
 UNION ALL SELECT 'today',$4::timestamptz,'infinity'::timestamptz FROM active WHERE enabled
), covered AS MATERIALIZED (
 SELECT r.kind,r.lo,r.hi,c.bucket_start
 FROM ranges r JOIN key_usage_covered c
 ON c.bucket_start>=r.lo AND c.bucket_start+interval '1 hour'<=r.hi
), boundaries AS (
 SELECT kind,lo AS gap_lo,COALESCE(min(bucket_start),hi) AS gap_hi
 FROM ranges r LEFT JOIN covered c USING(kind,lo,hi) GROUP BY kind,lo,hi
 UNION ALL
 SELECT kind,bucket_start+interval '1 hour',LEAD(bucket_start,1,hi) OVER(PARTITION BY kind ORDER BY bucket_start)
 FROM covered
), pieces AS (
 SELECT c.kind,h.api_key_id,h.actual_cost
 FROM covered c JOIN key_usage_hourly h USING(bucket_start) WHERE h.api_key_id=ANY($1)
 UNION ALL
 SELECT c.kind,q.api_key_id,q.actual_cost
 FROM covered c JOIN key_usage_changes q USING(bucket_start) WHERE q.api_key_id=ANY($1)
 UNION ALL
 SELECT b.kind,u.api_key_id,u.actual_cost FROM boundaries b
 CROSS JOIN LATERAL (
  SELECT api_key_id,SUM(actual_cost) AS actual_cost FROM usage_logs
  WHERE api_key_id=ANY($1) AND created_at>=b.gap_lo AND created_at<b.gap_hi
  GROUP BY api_key_id
 ) u WHERE b.gap_lo<b.gap_hi
), totals AS (
 SELECT api_key_id,COALESCE(SUM(actual_cost) FILTER(WHERE kind='total'),0) AS total,
 COALESCE(SUM(actual_cost) FILTER(WHERE kind='today'),0) AS today
 FROM pieces GROUP BY api_key_id
)
SELECT requested.id,COALESCE(t.total,0),COALESCE(t.today,0),active.enabled
FROM unnest($1::bigint[]) AS requested(id)
LEFT JOIN totals t ON t.api_key_id=requested.id CROSS JOIN active
`
