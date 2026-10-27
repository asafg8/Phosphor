<?php
// first-party legacy code (fixture variant APP=legacy74): runs on 7.4 (deprecations only) but uses constructs
// removed in PHP 8: each(), create_function(), curly-brace string offsets.
require '/usr/local/lib/phosphor-lab-kit/faults.php';
phosphor_lab_apply_faults();

function legacy74_first_pair($arr) {
    reset($arr);
    $pair = each($arr);
    return $pair ? $pair['key'] . '=' . $pair['value'] : '';
}

$upper = create_function('$s', 'return strtoupper($s);');
$name = isset($_GET['n']) ? (string) $_GET['n'] : 'phosphor';
$initial = $name{0};

header('Content-Type: text/plain');
echo legacy74_first_pair(array('app' => 'legacy74')) . "\n";
echo $upper($name) . ' ' . $initial . "\n";
