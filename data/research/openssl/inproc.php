<?php
// Exercises ext/openssl and ext/curl in ONE process, so a dual-OpenSSL clash (route A) shows up here.
$r = [];
$r['php'] = PHP_VERSION;
$r['openssl_text'] = OPENSSL_VERSION_TEXT;
$r['curl_ssl'] = curl_version()['ssl_version'];

$key = random_bytes(32); $iv = random_bytes(16);
$ct = openssl_encrypt('phosphor', 'aes-256-cbc', $key, 0, $iv);
$r['aes_roundtrip'] = openssl_decrypt($ct, 'aes-256-cbc', $key, 0, $iv) === 'phosphor';

$pk = openssl_pkey_new(['private_key_bits' => 2048, 'private_key_type' => OPENSSL_KEYTYPE_RSA]);
openssl_sign('msg', $sig, $pk, OPENSSL_ALGO_SHA256);
$pub = openssl_pkey_get_details($pk)['key'];
$r['rsa_sign_verify'] = openssl_verify('msg', $sig, $pub, OPENSSL_ALGO_SHA256) === 1;
$r['openssl_errors'] = [];
while ($e = openssl_error_string()) { $r['openssl_errors'][] = $e; }

foreach (['curl_https' => 'https://www.php.net/', 'curl_https2' => 'https://github.com/'] as $k => $url) {
    $ch = curl_init($url);
    curl_setopt_array($ch, [CURLOPT_RETURNTRANSFER => true, CURLOPT_NOBODY => true, CURLOPT_TIMEOUT => 20]);
    curl_exec($ch);
    $r[$k] = curl_getinfo($ch, CURLINFO_RESPONSE_CODE) . ' ' . curl_error($ch);
}

// PHP's own TLS stack (ext/openssl streams), same process, after curl used libssl.
$ctx = stream_context_create(['ssl' => ['verify_peer' => true, 'cafile' => '/etc/ssl/certs/ca-certificates.crt']]);
$fp = @stream_socket_client('ssl://www.php.net:443', $en, $es, 20, STREAM_CLIENT_CONNECT, $ctx);
$r['php_stream_tls'] = $fp ? 'ok' : "fail $en $es";

// openssl again after curl + streams (catches state corruption between the two libs)
$r['aes_after'] = openssl_decrypt(openssl_encrypt('x', 'aes-128-gcm', $key, 0, $iv12 = random_bytes(12), $tag), 'aes-128-gcm', $key, 0, $iv12, $tag) === 'x';
echo json_encode($r, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES), "\n";
