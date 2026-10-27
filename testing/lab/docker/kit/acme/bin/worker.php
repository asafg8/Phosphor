<?php
// acme cron worker (hidden PHP consumer): logs the PHP version it ran on.
echo date('c') . ' worker ' . PHP_VERSION . "\n";
