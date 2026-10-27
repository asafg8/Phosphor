<?php
// first-party page on the custom PHP 7.4 runtime of a mixed host (variant CUSTOM74=1)
require '/usr/local/lib/phosphor-lab-kit/faults.php';
phosphor_lab_apply_faults();
$n = isset($_GET['p']) ? (int) $_GET['p'] : 0;
echo "<html><body><h1>phosphor lab php74</h1><p>" . PHP_VERSION . " item " . $n . "</p></body></html>\n";
