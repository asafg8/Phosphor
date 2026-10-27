<?php
// legacy 5.6 app: register_globals era code, mysql_* and ereg (scan gate fixture)
require '/usr/local/lib/phosphor-lab-kit/faults.php';
phosphor_lab_apply_faults();
include __DIR__ . '/lib/page.php';
include __DIR__ . '/db.php';
include __DIR__ . '/util.php';
if (isset($page)) { /* relies on register_globals */ }
phosphor_page('legacy', isset($_GET['p']) ? (int) $_GET['p'] : 0);
