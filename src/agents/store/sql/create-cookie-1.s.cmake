.section ".note.GNU-stack","",%progbits
.section ".rodata"
.globl sql_create_cookie_1
.type sql_create_cookie_1,@object
sql_create_cookie_1:
.incbin "@CMAKE_CURRENT_SOURCE_DIR@/src/agents/store/sql/create-cookie-1.sql"
.byte 0
.size sql_create_cookie_1, .-sql_create_cookie_1
