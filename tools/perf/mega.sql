-- One account with 250,000 tracked titles, on top of the base fixture (seed.sql).
-- Adds 200,000 top-level titles so the account can track that many distinct works.
-- Never run against real data.
SET synchronous_commit = off;

INSERT INTO catalog_items(kind,provider,provider_id,title,unit,total,release_date,runtime_minutes,genres,credits,external_ids,image_url)
SELECT 'movie','tmdb','mm'||g,'Mega Movie '||g||' '||left(md5(g::text),6),'minutes',80+g%70,date '1950-01-01'+(g*7)%27000,80+g%70,
  ARRAY[(ARRAY['Drama','Action','Comedy','Thriller','Horror','Documentary'])[1+g%6]],ARRAY['Director '||g%6000,'Actor '||g%15000],jsonb_build_object('imdb','tt8'||g),
  'https://image.tmdb.org/t/p/w500/mm'||g||'.jpg'
FROM generate_series(1,90000) g;
INSERT INTO catalog_items(kind,provider,provider_id,title,unit,total,release_date,genres,credits,external_ids)
SELECT 'book','openlibrary','OLM'||g||'W','Mega Book '||g||' '||left(md5(g::text),6),'pages',120+g%700,date '1850-01-01'+(g*13)%60000,
  ARRAY[(ARRAY['Fiction','Fantasy','History','Science'])[1+g%4]],ARRAY['Author '||g%20000],jsonb_build_object('isbn','979'||lpad(g::text,10,'0'))
FROM generate_series(1,60000) g;
INSERT INTO catalog_items(kind,provider,provider_id,title,unit,total,release_date,genres,credits)
SELECT 'music','musicbrainz',lpad(to_hex(g),8,'0')||'-0000-4000-8000-000000000000','Mega Album '||g||' '||left(md5(g::text),6),'tracks',8+g%12,date '1960-01-01'+(g*9)%23000,ARRAY['Rock'],ARRAY['Band '||g%8000]
FROM generate_series(1,30000) g;
INSERT INTO catalog_items(kind,provider,provider_id,title,unit,release_date,genres,credits)
SELECT 'game','igdb','mega'||g,'Mega Game '||g||' '||left(md5(g::text),6),'minutes',date '1980-01-01'+(g*5)%16000,ARRAY['RPG'],ARRAY['Developer '||g%2000]
FROM generate_series(1,20000) g;

INSERT INTO users(username,display_name,password_hash,role) VALUES ('perf_mega','Perf Mega','','member');

INSERT INTO library_entries(user_id,title,media_type,publication_year,unit,total,status,progress,rating,catalog_item_id,created_at,last_activity_at,started_at,finished_at,completed_cycles)
SELECT u.id,c.title,c.kind,extract(year FROM c.release_date)::int,c.unit,c.total,st.status,
  CASE WHEN st.status='completed' AND c.total IS NOT NULL THEN c.total ELSE 0 END,
  CASE WHEN st.status='completed' AND c.id%3=0 THEN 5+c.id%6 END,c.id,
  now()-(c.id%3650)*interval '1 day',now()-((c.id*7)%1800)*interval '1 day',
  CASE WHEN st.status<>'planned' THEN now()-(c.id%3600)*interval '1 day' END,
  CASE WHEN st.status='completed' THEN now()-((c.id*7)%1800)*interval '1 day' END,
  CASE WHEN st.status='completed' THEN 1 ELSE 0 END
FROM users u
CROSS JOIN LATERAL (SELECT * FROM catalog_items c WHERE c.parent_id IS NULL ORDER BY hashint8(c.id*1000+u.id) LIMIT 250000) c
CROSS JOIN LATERAL (SELECT (ARRAY['completed','completed','in_progress','planned','planned','dropped','completed','in_progress'])[1+abs(hashint8(c.id+u.id*7))%8] AS status) st
WHERE u.username='perf_mega';

UPDATE library_entries e SET progress=1+e.id%99 FROM users u WHERE u.id=e.user_id AND u.username='perf_mega' AND e.media_type='series' AND e.status='in_progress';

INSERT INTO plays(user_id,entry_id,catalog_item_id,cycle,played_at,source)
SELECT e.user_id,e.id,ep.id,1,e.last_activity_at-((100-((ep.season_number-1)*20+ep.episode_number))*interval '1 day'),'manual'
FROM library_entries e JOIN users u ON u.id=e.user_id AND u.username='perf_mega'
JOIN catalog_items se ON se.parent_id=e.catalog_item_id
JOIN catalog_items ep ON ep.parent_id=se.id
WHERE e.media_type='series' AND e.status IN ('in_progress','completed')
  AND (ep.season_number-1)*20+ep.episode_number <= e.progress;

INSERT INTO plays(user_id,entry_id,catalog_item_id,cycle,played_at,source)
SELECT e.user_id,e.id,e.catalog_item_id,1,e.finished_at-(n-1)*interval '400 days','manual'
FROM library_entries e JOIN users u ON u.id=e.user_id AND u.username='perf_mega', generate_series(1,2) n
WHERE e.media_type<>'series' AND e.status='completed' AND (n=1 OR e.id%4=0);

UPDATE library_entries e SET next_item_id=(SELECT x.id FROM catalog_items x
  WHERE x.root_id=e.catalog_item_id AND x.kind='episode' AND x.season_number>0
  AND NOT EXISTS(SELECT 1 FROM plays p WHERE p.user_id=e.user_id AND p.entry_id=e.id AND p.cycle=e.cycle AND p.catalog_item_id=x.id)
  ORDER BY x.season_number,x.episode_number LIMIT 1)
FROM users u WHERE u.id=e.user_id AND u.username='perf_mega' AND e.media_type='series';

INSERT INTO activity_events(user_id,entry_id,kind,unit,status,progress,cycle,created_at)
SELECT e.user_id,e.id,'added',e.unit,e.status,e.progress,e.cycle,e.created_at FROM library_entries e JOIN users u ON u.id=e.user_id AND u.username='perf_mega';

ANALYZE;
