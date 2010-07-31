.section ".note.GNU-stack","",%progbits
.section ".rodata"
.globl sql_create_store_1
.type sql_create_store_1,@object
sql_create_store_1:
.incbin "@CMAKE_CURRENT_SOURCE_DIR@/src/agents/store/sql/create-store-1.sql"
.byte 0
.size sql_create_store_1, .-sql_create_store_1
