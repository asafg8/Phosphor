<?php
require '/usr/local/lib/phosphor-lab-kit/faults.php';
phosphor_lab_apply_faults();
include __DIR__ . '/lib/page.php';
phosphor_page('home', isset($_GET['p']) ? (int) $_GET['p'] : 0);
