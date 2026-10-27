#!/bin/sh
# EL/AL2023 php-fpm pools ship listen.acl_users; Docker's tmpfs /run has no POSIX ACLs, so php-fpm fails
# ("failed to read the ACL of the socket … Operation not supported") and nginx answers 502. Use owner/group/mode instead.
set -e
for f in /etc/php-fpm.d/*.conf; do
  sed -i 's/^listen.acl_users/;listen.acl_users/; s/^listen.acl_groups/;listen.acl_groups/; s/^;listen.owner = .*/listen.owner = nginx/; s/^;listen.group = .*/listen.group = nginx/; s/^;listen.mode = .*/listen.mode = 0660/' "$f"
done
