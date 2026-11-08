-- One sorted set keeps its generation marker even when there are no permits.
-- Missing/foreign generations require a PG-fenced rebuild, never an empty reset
-- by an individual worker. Only the original attempt member can be renewed.
local marker = '@generation:' .. ARGV[1]
if ARGV[5] == 'rebuild' then
  redis.call('DEL', KEYS[1])
  redis.call('ZADD', KEYS[1], '+inf', marker)
  local holds = cjson.decode(ARGV[6])
  for _, hold in ipairs(holds) do
    redis.call('ZADD', KEYS[1], hold.until_ms, 'a:' .. hold.attempt_id)
  end
elseif not redis.call('ZSCORE', KEYS[1], marker) then
  return -1
end
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now_ms)
local until_ms = tonumber(ARGV[4])
if until_ms <= now_ms then return -2 end
local member = 'a:' .. ARGV[3]
local current = redis.call('ZSCORE', KEYS[1], member)
if current then
  if tonumber(current) < until_ms then redis.call('ZADD', KEYS[1], until_ms, member) end
  return 1
end
if redis.call('ZCARD', KEYS[1]) - 1 >= tonumber(ARGV[2]) then return 0 end
redis.call('ZADD', KEYS[1], until_ms, member)
return 1
