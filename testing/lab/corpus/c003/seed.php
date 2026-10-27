<?php
// C-003: enable contrib + seed content with a full Drupal bootstrap (drush 8 needs the mysql CLI even to bootstrap,
// and the image can't install one). Run from the docroot: php /corpus-recipe/seed.php
define('DRUPAL_ROOT', getcwd());
$_SERVER += array('HTTP_HOST' => 'localhost', 'REMOTE_ADDR' => '127.0.0.1', 'SCRIPT_NAME' => '/index.php', 'REQUEST_METHOD' => 'GET', 'SERVER_SOFTWARE' => 'Apache');
require_once DRUPAL_ROOT . '/includes/bootstrap.inc';
drupal_bootstrap(DRUPAL_BOOTSTRAP_FULL);
$GLOBALS['user'] = user_load(1);
$mods = array('ctools', 'views', 'views_ui', 'token', 'pathauto', 'entity', 'libraries', 'date', 'date_popup', 'webform',
  'imce', 'jquery_update', 'admin_menu', 'features', 'googleanalytics');
if (!module_enable($mods, TRUE)) { fwrite(STDERR, "module_enable failed\n"); exit(1); }
if (!db_query("SELECT COUNT(*) FROM {node} WHERE type='article'")->fetchField()) {
  for ($i = 1; $i <= 5; $i++) {
    $n = (object) array('type' => 'article', 'title' => "Corpus Article $i", 'language' => LANGUAGE_NONE, 'uid' => 1, 'status' => 1);
    node_save($n);
  }
}
$nid = db_query("SELECT nid FROM {node} WHERE type='webform' AND title='Corpus Upload'")->fetchField();
if (!$nid) {
  module_load_include('inc', 'webform', 'includes/webform.components');
  $n = (object) array('type' => 'webform', 'title' => 'Corpus Upload', 'language' => LANGUAGE_NONE, 'uid' => 1, 'status' => 1);
  $n->webform = webform_node_defaults();
  $n->webform['components'] = array(
    1 => array('cid' => 1, 'pid' => 0, 'form_key' => 'name', 'name' => 'Name', 'type' => 'textfield', 'weight' => 0, 'value' => '', 'required' => 1, 'extra' => array()),
    2 => array('cid' => 2, 'pid' => 0, 'form_key' => 'doc', 'name' => 'Doc', 'type' => 'file', 'weight' => 1, 'value' => '', 'required' => 0,
      'extra' => array('filtering' => array('types' => array('txt', 'png'), 'size' => '1 MB', 'addextensions' => ''), 'scheme' => 'public', 'directory' => '', 'progress_indicator' => 'throbber', 'title_display' => 'before', 'description' => '', 'attributes' => array(), 'private' => 0, 'analysis' => FALSE)),
  );
  node_save($n);
  $nid = $n->nid;
}
file_put_contents('/var/www/html/sites/default/corpus-webform-nid', $nid);
variable_set('clean_url', 1);
echo 'drupal ', VERSION, ' php ', PHP_VERSION, ' modules ', count(module_list()), " webform $nid\n";
