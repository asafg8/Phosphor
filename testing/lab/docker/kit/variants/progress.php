<?php
// first-party upload-progress reader (fixture variant PROGRESS=1): uses session.upload_progress.
require '/usr/local/lib/phosphor-lab-kit/faults.php';
phosphor_lab_apply_faults();
session_start();
$key = ini_get('session.upload_progress.prefix') . (isset($_GET['id']) ? $_GET['id'] : 'phosphor');
header('Content-Type: application/json');
echo json_encode(isset($_SESSION[$key]) ? $_SESSION[$key] : array('state' => 'none')), "\n";
