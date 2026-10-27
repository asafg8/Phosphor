<?php
// C-002 Joomla 3.10 non-interactive install (Joomla 3 has no CLI installer): import core + sample SQL, add a Super
// User, write configuration.php, remove installation/. Run inside phosphor-corpus-c002: php /recipe/install.php
$root = '/var/www/html';
if (is_file("$root/configuration.php") && !is_dir("$root/installation")) { echo "already installed\n"; exit(0); }
for ($i = 0; $i < 60 && !is_dir("$root/installation"); $i++) sleep(1);
$db = new mysqli(getenv('JOOMLA_DB_HOST'), getenv('JOOMLA_DB_USER'), getenv('JOOMLA_DB_PASSWORD'), getenv('JOOMLA_DB_NAME'));
if ($db->connect_errno) { fwrite(STDERR, "db: {$db->connect_error}\n"); exit(1); }
foreach (['joomla.sql', 'sample_blog.sql'] as $f) {
    $sql = str_replace('#__', 'jos_', file_get_contents("$root/installation/sql/mysql/$f"));
    if (!$db->multi_query($sql)) { fwrite(STDERR, "$f: {$db->error}\n"); exit(1); }
    do { if ($r = $db->store_result()) $r->free(); } while ($db->more_results() && $db->next_result());
    if ($db->errno) { fwrite(STDERR, "$f: {$db->error}\n"); exit(1); }
}
$hash = password_hash('corpus-admin', PASSWORD_BCRYPT);
$now = gmdate('Y-m-d H:i:s');
$db->query("INSERT INTO jos_users (id,name,username,email,password,block,sendEmail,registerDate,lastvisitDate,activation,params,lastResetTime,resetCount,otpKey,otep,requireReset)
  VALUES (42,'Super User','admin','admin@corpus.test','$hash',0,1,'$now','$now','','{}','$now',0,'','',0)") or die($db->error);
$db->query("INSERT INTO jos_user_usergroup_map (user_id,group_id) VALUES (42,8)") or die($db->error);
// sample_blog references created_by 42 already (installer default id); nothing else to fix up
$cfg = file_get_contents("$root/installation/configuration.php-dist");
$set = ['sitename' => 'Corpus Joomla', 'host' => getenv('JOOMLA_DB_HOST'), 'user' => getenv('JOOMLA_DB_USER'),
    'password' => getenv('JOOMLA_DB_PASSWORD'), 'db' => getenv('JOOMLA_DB_NAME'), 'secret' => bin2hex(random_bytes(8)),
    'tmp_path' => "$root/tmp", 'log_path' => "$root/administrator/logs", 'mailfrom' => 'admin@corpus.test', 'fromname' => 'Corpus'];
foreach ($set as $k => $v) $cfg = preg_replace('/(public \$' . $k . '\s*=\s*)\'[^\']*\'/', '${1}\'' . addslashes($v) . '\'', $cfg);
file_put_contents("$root/configuration.php", $cfg);
// installer leftovers: Joomla refuses to run the site while installation/ exists
exec('rm -rf ' . escapeshellarg("$root/installation"));
exec('chown -R www-data:www-data ' . escapeshellarg($root));
echo "installed Joomla ", file_exists("$root/libraries/src/Version.php") ? 'ok' : '?', "\n";
