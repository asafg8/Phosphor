<?php
// acme queue consumer run by acme-queue.service (long-running CLI worker).
file_put_contents('/var/log/acme-queue.log', date('c') . ' queue start ' . PHP_VERSION . "\n", FILE_APPEND);
while (true) {
    sleep(30);
}
