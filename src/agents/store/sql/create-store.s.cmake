.section ".note.GNU-stack","",%progbits
.section ".rodata"
.globl sql_create_store
.type sql_create_store,@object
sql_create_store:
.incbin "@CMAKE_CURRENT_SOURCE_DIR@/src/agents/store/sql/create-store.sql"
.byte 0
.size sql_create_store, .-sql_create_store
