-- Synthetic load fixture for performance measurement. Never run against real data.
-- 100 accounts; ~106k top-level catalog titles plus 60k seasons and 1.2M episodes;
-- one heavy account (perf_user_1) with 10k tracked titles; ~1M plays overall.
SET synchronous_commit = off;

INSERT INTO users(username,display_name,password_hash,role)
SELECT 'perf_user_'||g, 'Perf User '||g, '', 'member' FROM generate_series(1,100) g;

INSERT INTO catalog_items(kind,provider,provider_id,title,unit,total,release_date,runtime_minutes,genres,credits,external_ids,image_url)
SELECT 'movie','tmdb','m'||g,'Synthetic Movie '||g||' '||left(md5(g::text),6),'minutes',80+g%70,date '1960-01-01'+(g*7)%24000,80+g%70,
  ARRAY[(ARRAY['Drama','Action','Comedy','Thriller','Science Fiction'])[1+g%5]],ARRAY['Director '||g%4000,'Actor '||g%9000],jsonb_build_object('imdb','tt'||g),
  'https://image.tmdb.org/t/p/w500/m'||g||'.jpg'
FROM generate_series(1,60000) g;

INSERT INTO catalog_items(kind,provider,provider_id,title,unit,total,release_date,runtime_minutes,genres,credits,external_ids,children_loaded,image_url)
SELECT 'series','tmdb','s'||g,'Synthetic Series '||g||' '||left(md5(g::text),6),'episodes',100,date '1990-01-01'+(g*3)%13000,42,
  ARRAY[(ARRAY['Drama','Comedy','Crime','Animation'])[1+g%4]],ARRAY['Creator '||g%2000],jsonb_build_object('imdb','tt9'||g,'tvdb',g::text),true,
  'https://image.tmdb.org/t/p/w500/s'||g||'.jpg'
FROM generate_series(1,12000) g;

INSERT INTO catalog_items(kind,provider,provider_id,parent_id,season_number,title,unit,total,release_date,children_loaded)
SELECT 'season','tmdb',c.provider_id||':'||n,c.id,n,'Season '||n,'episodes',20,c.release_date+(n-1)*365,true
FROM catalog_items c, generate_series(1,5) n WHERE c.kind='series';

-- The last season of every 20th show is still airing: some episodes are in the future.
INSERT INTO catalog_items(kind,provider,provider_id,parent_id,season_number,episode_number,title,unit,release_date,runtime_minutes)
SELECT 'episode','tmdb',s.provider_id||':'||e,s.id,s.season_number,e,'Episode '||e,'minutes',
  CASE WHEN s.season_number=5 AND split_part(s.provider_id,':',1)::text LIKE '%0' THEN current_date+(e-5)*7 ELSE s.release_date+e*7 END,42
FROM catalog_items s, generate_series(1,20) e WHERE s.kind='season';

INSERT INTO catalog_items(kind,provider,provider_id,title,unit,total,release_date,genres,credits,external_ids)
SELECT 'book','openlibrary','OL'||g||'W','Synthetic Book '||g||' '||left(md5(g::text),6),'pages',150+g%500,date '1900-01-01'+(g*11)%45000,
  ARRAY['Fiction'],ARRAY['Author '||g%7000],jsonb_build_object('isbn','978'||lpad(g::text,10,'0'))
FROM generate_series(1,20000) g;

INSERT INTO catalog_items(kind,provider,provider_id,title,unit,total,release_date,genres,credits)
SELECT 'anime','anilist',g::text,'Synthetic Anime '||g||' '||left(md5(g::text),6),'episodes',12+g%40,date '1980-01-01'+(g*5)%16000,ARRAY['Action'],ARRAY['Studio '||g%300]
FROM generate_series(1,8000) g;

INSERT INTO catalog_items(kind,provider,provider_id,title,unit,release_date,genres,credits)
SELECT 'game','igdb',g::text,'Synthetic Game '||g||' '||left(md5(g::text),6),'minutes',date '1985-01-01'+(g*5)%14000,ARRAY['Adventure'],ARRAY['Developer '||g%900]
FROM generate_series(1,6000) g;

-- Library entries: 10k for the heavy account, 1,000 for everyone else, picked by a
-- per-account hash so accounts overlap realistically.
INSERT INTO library_entries(user_id,title,media_type,publication_year,unit,total,status,progress,rating,catalog_item_id,created_at,last_activity_at,started_at,finished_at,completed_cycles)
SELECT u.id,c.title,c.kind,extract(year FROM c.release_date)::int,c.unit,c.total,st.status,
  CASE WHEN st.status='completed' AND c.total IS NOT NULL THEN c.total ELSE 0 END,
  CASE WHEN st.status='completed' AND c.id%3=0 THEN 5+c.id%6 END,c.id,
  now()-((c.id+u.id)%1800)*interval '1 day',now()-((c.id*u.id)%900)*interval '1 day',
  CASE WHEN st.status<>'planned' THEN now()-((c.id+u.id)%1700)*interval '1 day' END,
  CASE WHEN st.status='completed' THEN now()-((c.id*u.id)%900)*interval '1 day' END,
  CASE WHEN st.status='completed' THEN 1 ELSE 0 END
FROM users u
CROSS JOIN LATERAL (SELECT * FROM catalog_items c WHERE c.parent_id IS NULL
  ORDER BY hashint8(c.id*1000+u.id) LIMIT CASE WHEN u.username='perf_user_1' THEN 10000 ELSE 1000 END) c
CROSS JOIN LATERAL (SELECT (ARRAY['completed','completed','in_progress','planned','planned','dropped','completed','in_progress'])[1+abs(hashint8(c.id+u.id*7))%8] AS status) st
WHERE u.username LIKE 'perf_user_%';

-- Episode plays: all 100 for completed shows, a prefix for shows in progress.
UPDATE library_entries SET progress=1+id%99 WHERE media_type='series' AND status='in_progress';
INSERT INTO plays(user_id,entry_id,catalog_item_id,cycle,played_at,source)
SELECT e.user_id,e.id,ep.id,1,e.last_activity_at-((100-((ep.season_number-1)*20+ep.episode_number))*interval '1 day'),'manual'
FROM library_entries e
JOIN catalog_items se ON se.parent_id=e.catalog_item_id
JOIN catalog_items ep ON ep.parent_id=se.id
WHERE e.media_type='series' AND e.status IN ('in_progress','completed')
  AND (ep.season_number-1)*20+ep.episode_number <= e.progress;

-- One or two plays for completed single works.
INSERT INTO plays(user_id,entry_id,catalog_item_id,cycle,played_at,source)
SELECT e.user_id,e.id,e.catalog_item_id,1,e.finished_at-(n-1)*interval '400 days','manual'
FROM library_entries e, generate_series(1,2) n
WHERE e.media_type<>'series' AND e.status='completed' AND (n=1 OR e.id%4=0);

UPDATE library_entries e SET next_item_id=(SELECT x.id FROM catalog_items x
  WHERE x.root_id=e.catalog_item_id AND x.kind='episode' AND x.season_number>0
  AND NOT EXISTS(SELECT 1 FROM plays p WHERE p.user_id=e.user_id AND p.entry_id=e.id AND p.cycle=e.cycle AND p.catalog_item_id=x.id)
  ORDER BY x.season_number,x.episode_number LIMIT 1)
WHERE e.media_type='series';

INSERT INTO activity_events(user_id,entry_id,kind,unit,status,progress,cycle,created_at)
SELECT user_id,id,'added',unit,status,progress,cycle,created_at FROM library_entries;

ANALYZE;
