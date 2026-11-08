if not redis.call('ZSCORE', KEYS[1], '@generation:' .. ARGV[1]) then return -1 end
redis.call('ZREM', KEYS[1], 'a:' .. ARGV[2])
return 1
