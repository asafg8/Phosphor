<?php
/*
Plugin Name: Old Gallery
Description: an old third-party plugin still calling ereg() (removed in PHP 7.0).
Version: 0.9
*/
function old_gallery_is_image($name) {
    return ereg('\.(jpe?g|png|gif)$', $name);
}
