.section ".rodata"
.globl sql_create_store
.type sql_create_store,@object
sql_create_store:
.incbin "src/agents/store/create-store.sql"
.byte 0
.size sql_create_store, .-sql_create_store
