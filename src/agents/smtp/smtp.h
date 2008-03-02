#ifndef SMTP_H
#define SMTP_H

/* SMTP Extensions */
#define EXT_DSN         (1<<0)
#define EXT_PIPELINING  (1<<1)
#define EXT_8BITMIME    (1<<2)
#define EXT_AUTH_LOGIN  (1<<3)
#define EXT_CHUNKING    (1<<4)
#define EXT_BINARYMIME  (1<<5)
#define EXT_SIZE        (1<<6)
#define EXT_ETRN        (1<<7)
#define EXT_TLS         (1<<8)

#endif
