-- Remove legacy placeholder data that used to be inserted by
-- 000_create_core_tables.sql on fresh database initialization.
--
-- Keep the cleanup narrowly scoped so user-created or edited channels are not
-- removed accidentally.

DELETE a
FROM `abilities` a
JOIN `channels` c ON c.`id` = a.`channel_id`
WHERE c.`id` = 1
  AND c.`type` = 1
  AND c.`key` = 'sk-placeholder'
  AND c.`name` = 'test-openai'
  AND c.`base_url` = 'https://api.openai.com'
  AND c.`models` = 'gpt-3.5-turbo,gpt-4'
  AND c.`group` = 'default'
  AND a.`group` = 'default'
  AND a.`model` IN ('gpt-3.5-turbo', 'gpt-4');

DELETE FROM `channels`
WHERE `id` = 1
  AND `type` = 1
  AND `key` = 'sk-placeholder'
  AND `name` = 'test-openai'
  AND `base_url` = 'https://api.openai.com'
  AND `models` = 'gpt-3.5-turbo,gpt-4'
  AND `group` = 'default';
