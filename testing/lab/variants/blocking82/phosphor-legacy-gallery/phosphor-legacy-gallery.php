<?php
/*
Plugin Name: Phosphor Legacy Gallery
Description: third-party plugin that works on PHP 7.4 but uses functions REMOVED in PHP 8.0 (fixture variant PLUGIN=blocking82).
Version: 1.2
*/
function phosphor_legacy_gallery_first($items) {
    reset($items);
    $pair = each($items);                                       // each(): removed in 8.0
    return $pair ? $pair['value'] : null;
}
function phosphor_legacy_gallery_sorter() {
    return create_function('$a,$b', 'return strcmp($a, $b);');  // create_function(): removed in 8.0
}
