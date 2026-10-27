-----BEGIN PGP SIGNED MESSAGE-----
Hash: SHA512

Format: 3.0 (quilt)
Source: php7.4
Binary: libapache2-mod-php7.4, libphp7.4-embed, php7.4, php7.4-bcmath, php7.4-bz2, php7.4-cgi, php7.4-cli, php7.4-common, php7.4-curl, php7.4-dba, php7.4-dev, php7.4-enchant, php7.4-fpm, php7.4-gd, php7.4-gmp, php7.4-imap, php7.4-interbase, php7.4-intl, php7.4-json, php7.4-ldap, php7.4-litespeed, php7.4-mbstring, php7.4-mysql, php7.4-odbc, php7.4-opcache, php7.4-pgsql, php7.4-phpdbg, php7.4-pspell, php7.4-readline, php7.4-snmp, php7.4-soap, php7.4-sqlite3, php7.4-sybase, php7.4-tidy, php7.4-xml, php7.4-xmlrpc, php7.4-xsl, php7.4-zip
Architecture: any all
Version: 1:7.4.33-31+ubuntu22.04.1+deb.sury.org+1
Maintainer: Debian PHP Maintainers <team+pkg-php@tracker.debian.org>
Uploaders: Ondřej Surý <ondrej@debian.org>, Lior Kaplan <kaplan@debian.org>
Homepage: http://www.php.net/
Standards-Version: 4.5.0.0
Vcs-Browser: https://salsa.debian.org/php-team/php/-/tree/debian/main/7.4
Vcs-Git: https://salsa.debian.org/php-team/php.git -b debian/main/7.4
Testsuite: autopkgtest
Testsuite-Triggers: apache2, wget
Build-Depends: debhelper (>= 9.20160709~), dh-apache2, dpkg-dev (>= 1.16.1~), apache2-bin (>= 2.4), apache2-dev (>= 2.4), autoconf (>= 2.63), automake, bison, chrpath, default-libmysqlclient-dev | libmysqlclient-dev, firebird-dev [!hurd-any !m68k !hppa !ppc64] | firebird2.5-dev [!hurd-any !m68k !hppa !ppc64] | firebird2.1-dev [!hurd-any !m68k !hppa !ppc64], flex, freetds-dev, libacl1-dev, libapparmor-dev [linux-any], libapr1-dev (>= 1.2.7-8), libargon2-dev | libargon2-0-dev, libbz2-dev, libc-client-dev, libcurl4-openssl-dev | libcurl-dev, libdb-dev, libedit-dev (>= 2.11-20080614-4), libenchant-2-dev | libenchant-dev, libevent-dev (>= 1.4.11), libexpat1-dev (>= 1.95.2-2.1), libffi-dev, libfreetype6-dev, libgcrypt20-dev (>> 1.6.3) | libgcrypt11-dev (<< 1.5.4), libgd-dev (>= 2.1.0) | libgd2-dev, libglib2.0-dev, libgmp3-dev, libicu-dev, libjpeg-dev | libjpeg62-dev, libkrb5-dev, libldap2-dev, liblmdb-dev, libmagic-dev, libmhash-dev (>= 0.8.8), libnss-myhostname [linux-any], libonig-dev, libpam0g-dev, libpcre2-dev (>= 10.30), libpng-dev, libpq-dev, libpspell-dev, libqdbm-dev, libsasl2-dev, libsnmp-dev, libsodium-dev, libsqlite3-dev, libssl-dev, libsystemd-dev [linux-any], libtidy-dev (>= 1:5.2.0), libtool (>= 2.2), libwebp-dev, libwrap0-dev, libxml2-dev, libxmlrpc-epi-dev, libxslt1-dev (>= 1.0.18), libzip-dev (>= 1.0.0), locales-all | language-pack-de, netbase, netcat-openbsd, php-common, re2c, systemtap-sdt-dev, tzdata, unixodbc-dev, zlib1g-dev
Build-Conflicts: bind-dev, libxmlrpc-core-c3-dev
Package-List:
 libapache2-mod-php7.4 deb httpd optional arch=any
 libphp7.4-embed deb php optional arch=any
 php7.4 deb php optional arch=all
 php7.4-bcmath deb php optional arch=any
 php7.4-bz2 deb php optional arch=any
 php7.4-cgi deb php optional arch=any
 php7.4-cli deb php optional arch=any
 php7.4-common deb php optional arch=any
 php7.4-curl deb php optional arch=any
 php7.4-dba deb php optional arch=any
 php7.4-dev deb php optional arch=any
 php7.4-enchant deb php optional arch=any
 php7.4-fpm deb php optional arch=any
 php7.4-gd deb php optional arch=any
 php7.4-gmp deb php optional arch=any
 php7.4-imap deb php optional arch=any
 php7.4-interbase deb php optional arch=any
 php7.4-intl deb php optional arch=any
 php7.4-json deb php optional arch=any
 php7.4-ldap deb php optional arch=any
 php7.4-litespeed deb php optional arch=any
 php7.4-mbstring deb php optional arch=any
 php7.4-mysql deb php optional arch=any
 php7.4-odbc deb php optional arch=any
 php7.4-opcache deb php optional arch=any
 php7.4-pgsql deb php optional arch=any
 php7.4-phpdbg deb php optional arch=any
 php7.4-pspell deb php optional arch=any
 php7.4-readline deb php optional arch=any
 php7.4-snmp deb php optional arch=any
 php7.4-soap deb php optional arch=any
 php7.4-sqlite3 deb php optional arch=any
 php7.4-sybase deb php optional arch=any
 php7.4-tidy deb php optional arch=any
 php7.4-xml deb php optional arch=any
 php7.4-xmlrpc deb php optional arch=any
 php7.4-xsl deb php optional arch=all
 php7.4-zip deb php optional arch=any
Checksums-Sha1:
 4d3152b2339332b4eef2c12931931d4a1245fdab 10420144 php7.4_7.4.33.orig.tar.xz
 1701962351f13c1af1f29bde45eb9515747bc4ee 833 php7.4_7.4.33.orig.tar.xz.asc
 8e6d9e880ae125de9d6aebfa67703fa4e53d8e6b 213824 php7.4_7.4.33-31+ubuntu22.04.1+deb.sury.org+1.debian.tar.xz
Checksums-Sha256:
 924846abf93bc613815c55dd3f5809377813ac62a9ec4eb3778675b82a27b927 10420144 php7.4_7.4.33.orig.tar.xz
 569a01c7c605a4571fdf7dfadfff4215cc4a63ea5d474c7ec92bd7b4fecfffcb 833 php7.4_7.4.33.orig.tar.xz.asc
 9bb6273e01ea4c127ebe95cedd0cedf6bc3f6658b4ac5e7c830f91ae7e7e3749 213824 php7.4_7.4.33-31+ubuntu22.04.1+deb.sury.org+1.debian.tar.xz
Files:
 f098632163cd47f2c1ffe2bdc6ef1ff2 10420144 php7.4_7.4.33.orig.tar.xz
 306dca821388f20fa55324960d82f427 833 php7.4_7.4.33.orig.tar.xz.asc
 5027f00954f6439b43ed50eb840cf32e 213824 php7.4_7.4.33-31+ubuntu22.04.1+deb.sury.org+1.debian.tar.xz

-----BEGIN PGP SIGNATURE-----

iQKTBAEBCgB9FiEEahqteuW0AcbiWaeyVwZ7qhMUx/wFAmrYysNfFIAAAAAALgAo
aXNzdWVyLWZwckBub3RhdGlvbnMub3BlbnBncC5maWZ0aGhvcnNlbWFuLm5ldDZB
MUFBRDdBRTVCNDAxQzZFMjU5QTdCMjU3MDY3QkFBMTMxNEM3RkMACgkQVwZ7qhMU
x/zVJA/9Ex2AWgpBHLijcDuW8k2SCz/CuwwrGPnzwqJ3pRxudYE5Uu/L8OUkmeV7
gMQBBePhsrsl4Dgtvf3nA/q0NjLQcAtN3YWYNAPY+rLSP6RLzPZ62Wvd10aOg/l+
CVo9w9T3LNu1MAzHVFFsNt/kpa8sSMDFNUh6JD64gn53RQwsFgMe8Nd50nqphRHg
cK8sJWZTvBGZBXMJtGXpWb0pHhnAtlrTfW2nd+4mLPt3a1EeWUQ/SalvVHTUGn3D
txo0e4bH3g2hgoy9Jj5NOOV+qND3LQU2wrNpZX0q8AF/ajFhHYCSt5ssAvAXbvhY
fxClEruLC5tp4toSJKh7921hfFkGuoTIbKPw/nAHHha29EH/0g+egNxygpKBcjWn
mhH9C/62mxMpUAB1OJSkpEOQxdZuveNJNoq5CaUaE6MP7nZECwcqDqp+cEVGOtv/
yUs/uqkmb1HriCeuOQjlMrD5bf2XEcWuJcc8kyS70PxJNFcA+OYUQopto8SrpqU3
vRE0YCr55KKJEttaojsHekRD63cyunCGTlmq2Yxjc0PlV8sbGGaBZ8jPAPifRX05
4s3TlbhVNTbcf0am5cs1onoG002PiGX+hStr9dIe+4Kt+qs5d6/fBaPWMYcFfuCd
UPmdIGxrFljpt4TseoNZ595bEqpADR3Gkh2PFxwpwZbxVjUq6z4=
=NXHh
-----END PGP SIGNATURE-----
