<?php
function legacy_db() {
    if (!function_exists('mysql_connect')) {
        return false;
    }
    $link = @mysql_connect('127.0.0.1', 'app', 'app');
    if (!$link) {
        return false;
    }
    mysql_select_db('legacy', $link);
    $r = mysql_query('SELECT 1', $link);
    return mysql_fetch_assoc($r);
}
