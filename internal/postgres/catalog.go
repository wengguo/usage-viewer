package postgres

const connectivityProbeSQL = `
SELECT 1
`

const roleEvidenceSQL = `
SELECT
    CURRENT_USER::text,
    role.rolcanlogin,
    role.rolsuper,
    role.rolinherit,
    role.rolcreaterole,
    role.rolcreatedb,
    role.rolreplication,
    role.rolbypassrls,
    pg_catalog.has_database_privilege(
        CURRENT_USER,
        pg_catalog.current_database(),
        'CONNECT'
    ),
    pg_catalog.has_schema_privilege(
        CURRENT_USER,
        'public',
        'USAGE'
    )
FROM pg_catalog.pg_roles AS role
WHERE role.rolname = CURRENT_USER
`

const readOnlyEvidenceSQL = `
SELECT pg_catalog.current_setting('default_transaction_read_only')::boolean
`

const authorityEvidenceSQL = `
WITH RECURSIVE
self_role AS (
    SELECT role.oid
    FROM pg_catalog.pg_roles AS role
    WHERE role.rolname = CURRENT_USER
),
effective_memberships(role_oid) AS (
    SELECT membership.roleid
    FROM pg_catalog.pg_auth_members AS membership
    JOIN self_role ON self_role.oid = membership.member
    UNION
    SELECT membership.roleid
    FROM pg_catalog.pg_auth_members AS membership
    JOIN effective_memberships
      ON effective_memberships.role_oid = membership.member
),
user_namespaces AS (
    SELECT namespace.oid, namespace.nspname, namespace.nspowner, namespace.nspacl
    FROM pg_catalog.pg_namespace AS namespace
    WHERE namespace.nspname <> 'pg_catalog'
      AND namespace.nspname <> 'information_schema'
      AND namespace.nspname NOT LIKE 'pg_toast%'
      AND namespace.nspname NOT LIKE 'pg_temp_%'
),
user_objects AS (
    SELECT relation.oid, relation.relname, relation.relnamespace, relation.relowner, relation.relkind
    FROM pg_catalog.pg_class AS relation
    JOIN user_namespaces ON user_namespaces.oid = relation.relnamespace
),
user_relations AS (
    SELECT user_objects.oid, user_objects.relname, user_objects.relnamespace, user_objects.relowner, user_objects.relkind
    FROM user_objects
    WHERE user_objects.relkind IN ('r', 'p', 'v', 'm', 'f')
),
user_sequences AS (
    SELECT user_objects.oid, user_objects.relowner
    FROM user_objects
    WHERE user_objects.relkind = 'S'
),
required_columns(schema_name, table_name, column_name) AS (
    SELECT required.schema_name, required.table_name, required.column_name
    FROM pg_catalog.unnest($1::text[], $2::text[], $3::text[])
      AS required(schema_name, table_name, column_name)
)
SELECT
    (
        SELECT pg_catalog.count(*)::bigint
        FROM pg_catalog.pg_roles AS candidate
        CROSS JOIN self_role
        WHERE candidate.oid <> self_role.oid
          AND (
              candidate.oid IN (SELECT role_oid FROM effective_memberships)
              OR pg_catalog.pg_has_role(CURRENT_USER, candidate.oid, 'MEMBER')
          )
    ),
    (
        SELECT pg_catalog.count(*)::bigint
        FROM pg_catalog.pg_database AS database
        CROSS JOIN self_role
        WHERE database.datname = pg_catalog.current_database()
          AND (
              database.datdba = self_role.oid
              OR pg_catalog.pg_has_role(CURRENT_USER, database.datdba, 'MEMBER')
          )
    ),
    (
        SELECT pg_catalog.count(*)::bigint
        FROM user_namespaces AS namespace
        CROSS JOIN self_role
        WHERE namespace.nspowner = self_role.oid
           OR pg_catalog.pg_has_role(CURRENT_USER, namespace.nspowner, 'MEMBER')
    ),
    (
        SELECT pg_catalog.count(*)::bigint
        FROM user_objects AS owned_relation
        CROSS JOIN self_role
        WHERE owned_relation.relowner = self_role.oid
           OR pg_catalog.pg_has_role(CURRENT_USER, owned_relation.relowner, 'MEMBER')
    ),
    (
        SELECT (
            CASE WHEN pg_catalog.has_database_privilege(
                CURRENT_USER,
                pg_catalog.current_database(),
                'CREATE'
            ) THEN 1 ELSE 0 END
            +
            CASE WHEN pg_catalog.has_database_privilege(
                CURRENT_USER,
                pg_catalog.current_database(),
                'TEMP'
            ) THEN 1 ELSE 0 END
        )::bigint
    ),
    (
        SELECT pg_catalog.count(*)::bigint
        FROM user_namespaces AS namespace
        WHERE pg_catalog.has_schema_privilege(
            CURRENT_USER,
            namespace.oid,
            'CREATE'
        )
    ),
    (
        (
            SELECT pg_catalog.count(*)::bigint
            FROM user_relations AS relation
            WHERE pg_catalog.has_table_privilege(CURRENT_USER, relation.oid, 'INSERT')
               OR pg_catalog.has_table_privilege(CURRENT_USER, relation.oid, 'UPDATE')
               OR pg_catalog.has_table_privilege(CURRENT_USER, relation.oid, 'DELETE')
               OR pg_catalog.has_table_privilege(CURRENT_USER, relation.oid, 'TRUNCATE')
               OR pg_catalog.has_table_privilege(CURRENT_USER, relation.oid, 'REFERENCES')
               OR pg_catalog.has_table_privilege(CURRENT_USER, relation.oid, 'TRIGGER')
        )
        +
        (
            SELECT pg_catalog.count(*)::bigint
            FROM user_relations AS relation
            JOIN pg_catalog.pg_attribute AS attribute
              ON attribute.attrelid = relation.oid
             AND attribute.attnum > 0
             AND NOT attribute.attisdropped
            WHERE pg_catalog.has_column_privilege(CURRENT_USER, relation.oid, attribute.attnum, 'INSERT')
               OR pg_catalog.has_column_privilege(CURRENT_USER, relation.oid, attribute.attnum, 'UPDATE')
               OR pg_catalog.has_column_privilege(CURRENT_USER, relation.oid, attribute.attnum, 'REFERENCES')
        )
    ),
    (
        SELECT pg_catalog.count(*)::bigint
        FROM user_sequences AS sequence
        WHERE pg_catalog.has_sequence_privilege(CURRENT_USER, sequence.oid, 'SELECT')
           OR pg_catalog.has_sequence_privilege(CURRENT_USER, sequence.oid, 'USAGE')
           OR pg_catalog.has_sequence_privilege(CURRENT_USER, sequence.oid, 'UPDATE')
    ),
    (
        (
            SELECT pg_catalog.count(*)::bigint
            FROM pg_catalog.pg_database AS database
            CROSS JOIN LATERAL pg_catalog.aclexplode(
                COALESCE(database.datacl, pg_catalog.acldefault('d', database.datdba))
            ) AS privilege
            WHERE database.datname = pg_catalog.current_database()
              AND privilege.privilege_type = 'CONNECT'
              AND privilege.is_grantable
              AND CASE
                    WHEN privilege.grantee = 0 THEN true
                    ELSE pg_catalog.pg_has_role(CURRENT_USER, privilege.grantee, 'MEMBER')
                  END
        )
        +
        (
            SELECT pg_catalog.count(*)::bigint
            FROM user_namespaces AS namespace
            CROSS JOIN LATERAL pg_catalog.aclexplode(
                COALESCE(namespace.nspacl, pg_catalog.acldefault('n', namespace.nspowner))
            ) AS privilege
            WHERE namespace.nspname = 'public'
              AND privilege.privilege_type = 'USAGE'
              AND privilege.is_grantable
              AND CASE
                    WHEN privilege.grantee = 0 THEN true
                    ELSE pg_catalog.pg_has_role(CURRENT_USER, privilege.grantee, 'MEMBER')
                  END
        )
        +
        (
            SELECT pg_catalog.count(*)::bigint
            FROM required_columns AS required
            JOIN pg_catalog.pg_namespace AS namespace
              ON namespace.nspname = required.schema_name
            JOIN pg_catalog.pg_class AS relation
              ON relation.relnamespace = namespace.oid
             AND relation.relname = required.table_name
            JOIN pg_catalog.pg_attribute AS attribute
              ON attribute.attrelid = relation.oid
             AND attribute.attname = required.column_name
             AND attribute.attnum > 0
             AND NOT attribute.attisdropped
            CROSS JOIN LATERAL pg_catalog.aclexplode(attribute.attacl) AS privilege
            WHERE privilege.privilege_type = 'SELECT'
              AND privilege.is_grantable
              AND CASE
                    WHEN privilege.grantee = 0 THEN true
                    ELSE pg_catalog.pg_has_role(CURRENT_USER, privilege.grantee, 'MEMBER')
                  END
        )
    ),
    (
        SELECT pg_catalog.count(*)::bigint
        FROM pg_catalog.pg_proc AS routine
        JOIN user_namespaces AS namespace ON namespace.oid = routine.pronamespace
        WHERE routine.prosecdef
          AND pg_catalog.has_function_privilege(CURRENT_USER, routine.oid, 'EXECUTE')
    ),
    (
        SELECT pg_catalog.count(*)::bigint
        FROM pg_catalog.pg_largeobject_metadata AS large_object
        CROSS JOIN self_role
        WHERE large_object.lomowner = self_role.oid
           OR pg_catalog.pg_has_role(CURRENT_USER, large_object.lomowner, 'MEMBER')
           OR EXISTS (
                SELECT 1
                FROM pg_catalog.aclexplode(large_object.lomacl) AS privilege
                WHERE privilege.privilege_type = 'UPDATE'
                  AND CASE
                        WHEN privilege.grantee = 0 THEN true
                        ELSE pg_catalog.pg_has_role(CURRENT_USER, privilege.grantee, 'MEMBER')
                      END
           )
    ),
    (
        SELECT pg_catalog.count(*)::bigint
        FROM pg_catalog.pg_proc AS routine
        JOIN user_namespaces AS namespace ON namespace.oid = routine.pronamespace
        CROSS JOIN self_role
        WHERE routine.proowner = self_role.oid
           OR pg_catalog.pg_has_role(CURRENT_USER, routine.proowner, 'MEMBER')
    ),
    (
        SELECT pg_catalog.count(*)::bigint
        FROM user_relations AS relation
        WHERE pg_catalog.has_table_privilege(CURRENT_USER, relation.oid, 'SELECT')
    ),
    (
        SELECT pg_catalog.count(*)::bigint
        FROM user_relations AS relation
        JOIN user_namespaces AS namespace ON namespace.oid = relation.relnamespace
        JOIN pg_catalog.pg_attribute AS attribute
          ON attribute.attrelid = relation.oid
         AND attribute.attnum > 0
         AND NOT attribute.attisdropped
        WHERE pg_catalog.has_column_privilege(
            CURRENT_USER,
            relation.oid,
            attribute.attnum,
            'SELECT'
        )
          AND NOT EXISTS (
              SELECT 1
              FROM required_columns AS required
              WHERE required.schema_name = namespace.nspname
                AND required.table_name = relation.relname
                AND required.column_name = attribute.attname
          )
    )
`

const relationEvidenceSQL = `
WITH required_relations(schema_name, relation_name, position) AS (
    SELECT required.schema_name, required.relation_name, required.position
    FROM pg_catalog.unnest($1::text[], $2::text[])
      WITH ORDINALITY AS required(schema_name, relation_name, position)
)
SELECT
    required.schema_name,
    required.relation_name,
    COALESCE(relation.relkind::text, '')
FROM required_relations AS required
LEFT JOIN pg_catalog.pg_namespace AS namespace
  ON namespace.nspname = required.schema_name
LEFT JOIN pg_catalog.pg_class AS relation
  ON relation.relnamespace = namespace.oid
 AND relation.relname = required.relation_name
ORDER BY required.position
`

const columnEvidenceSQL = `
WITH required_columns(schema_name, table_name, column_name, position) AS (
    SELECT required.schema_name, required.table_name, required.column_name, required.position
    FROM pg_catalog.unnest($1::text[], $2::text[], $3::text[])
      WITH ORDINALITY AS required(schema_name, table_name, column_name, position)
)
SELECT
    required.schema_name,
    required.table_name,
    required.column_name,
    COALESCE(type.typname, ''),
    NOT COALESCE(attribute.attnotnull, false),
    COALESCE(
        pg_catalog.has_column_privilege(
            CURRENT_USER,
            relation.oid,
            attribute.attnum,
            'SELECT'
        ),
        false
    )
FROM required_columns AS required
LEFT JOIN pg_catalog.pg_namespace AS namespace
  ON namespace.nspname = required.schema_name
LEFT JOIN pg_catalog.pg_class AS relation
  ON relation.relnamespace = namespace.oid
 AND relation.relname = required.table_name
LEFT JOIN pg_catalog.pg_attribute AS attribute
  ON attribute.attrelid = relation.oid
 AND attribute.attname = required.column_name
 AND attribute.attnum > 0
 AND NOT attribute.attisdropped
LEFT JOIN pg_catalog.pg_type AS type
  ON type.oid = attribute.atttypid
ORDER BY required.position
`
