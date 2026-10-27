<?php
// C-003: Drupal 7 programmatic install (what drush si does) without a mysql client: the bullseye-based image can no
// longer apt-get one (debian-security pool 404s, 2026-10). Run: php /corpus-recipe/site-install.php (cwd = docroot).
define('DRUPAL_ROOT', getcwd());
define('MAINTENANCE_MODE', 'install');
$_SERVER['HTTP_HOST'] = 'localhost'; $_SERVER['REMOTE_ADDR'] = '127.0.0.1'; $_SERVER['SCRIPT_NAME'] = '/install.php';
$_SERVER['REQUEST_METHOD'] = 'GET'; $_SERVER['SERVER_SOFTWARE'] = 'Apache';
require_once DRUPAL_ROOT . '/includes/install.core.inc';
install_drupal(array(
  'interactive' => FALSE,
  'parameters' => array('profile' => 'standard', 'locale' => 'en'),
  'forms' => array(
    'install_settings_form' => array('driver' => 'mysql', 'mysql' => array('database' => 'app', 'username' => 'app',
      'password' => 'app-pass', 'host' => 'c003-db', 'port' => '', 'prefix' => '', 'driver' => 'mysql'),
      'op' => 'Save and continue'),
    'install_configure_form' => array('site_name' => 'Corpus Drupal', 'site_mail' => 'admin@corpus.test',
      'account' => array('name' => 'admin', 'mail' => 'admin@corpus.test', 'pass' => array('pass1' => 'corpus-admin', 'pass2' => 'corpus-admin')),
      'clean_url' => 1,
      'date_default_timezone' => 'UTC', 'update_status_module' => array(), 'op' => 'Save and continue'),
  ),
));
echo "installed\n";
