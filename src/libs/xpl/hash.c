/* Bongo Project licensing applies to this file, see COPYING
 * (C) 2007 Alex Hudson
 */

/** \file
 *  Implementation of XPL hash API: access to gcrypt's MD5 and SHA1 algorithms
 */

#include <config.h>
#include <libintl.h>
#include <xplhash.h>
#include <memmgr.h>
#include <gcrypt.h>
#include <libintl.h>
#include <locale.h>
GCRY_THREAD_OPTION_PTHREAD_IMPL;

/** 
 * Initialise the XPL library. Should be called once in each program that
 * uses XPL.
 */
void
XplInit(void)
{
	XplHashInit();

	// initialize gettext
	setlocale (LC_ALL, "");
	bindtextdomain (PACKAGE, LOCALEDIR);
	textdomain (PACKAGE);
}

/**
 * Initialise the hashing subsystem for a program. This is usually part of
 * the XplInit() process - this is sort-of private. 
 */
void 
XplHashInit(void) 
{
	//const char *gcry_version;
	gcry_control (GCRYCTL_SET_THREAD_CBS, &gcry_threads_pthread);
	//gcry_version = gcry_check_version(NULL);
	gcry_control (GCRYCTL_DISABLE_SECMEM, 0);
	gcry_control (GCRYCTL_SET_RANDOM_SEED_FILE, XPL_DEFAULT_RANDSEED_PATH);
	gcry_control (GCRYCTL_INITIALIZATION_FINISHED, 0);
}

/**
 * Create a new context to start a hash digest.
 * \param	ctx	xpl_hash_context to use
 * \param 	type 	Hashing algorithm to use
 * \return		A new hash context for further use
 */
void 
XplHashNew(xpl_hash_context *ctx, xpl_hash_type type) 
{
	ctx->type = type;
	switch(type) {
		case XPLHASH_MD5:
			ctx->buffer_size = XPLHASH_MD5BYTES_LENGTH;
			break;
		case XPLHASH_SHA1:
			ctx->buffer_size = XPLHASH_SHA1BYTES_LENGTH;
			break;
		default:
			// unknown hash type - FIXME: ERROR
			ctx->buffer_size = 0;
	}
	gcry_md_open(&ctx->gcrypt_context, type, 0);
}

/**
 * Write data into the context to be hashed.
 * \param 	context	Hash context to be used
 * \param	buffer	Pointer to the data to be written
 * \param	length	Amount of data from buffer to be written
 */
void 
XplHashWrite(xpl_hash_context *context, const void *buffer, size_t length) 
{	
	gcry_md_write(context->gcrypt_context, buffer, length);
}

/**
 * Finalise the hash, and return the hash in raw format
 * See XplHashFinal()
 * \param	context	Hash context to be used
 * \param	buffer	Buffer into which the hash will be written
 * \param	length 	Amount of data to write
 */
void 
XplHashFinalBytes(xpl_hash_context *context, unsigned char *buffer, size_t length) 
{
	memcpy(buffer, gcry_md_read(context->gcrypt_context, 0), min(context->buffer_size, length));
	gcry_md_close(context->gcrypt_context);
}

/**
 * Finalise the hash, and return the hash encoded in string format
 * See XplHashFinalBytes()
 * \param	context	Hash context to be used
 * \param 	strcase	Whether or not the string should use upper case
 * \param	buffer	Buffer into which the hash will be written
 * \param	length	Amount of data from buffer to be written
 */
void 
XplHashFinal(xpl_hash_context *context, xpl_hash_stringcase strcase, char *buffer, size_t length) 
{
	char format[5];
	unsigned char *digest;
	char *p;
	unsigned int i;
	
	memcpy(format, "%02X\0", 5);
	if (strcase == XPLHASH_LOWERCASE) 
		format[3] = 'x';
	
	digest = MemMalloc(context->buffer_size);
	memcpy(digest, gcry_md_read(context->gcrypt_context, 0), context->buffer_size);
	gcry_md_close(context->gcrypt_context);
	
	for (i = 0, p = buffer; i < context->buffer_size && p < buffer+length-1; i++, p += 2) {
		sprintf((char *)p, format, digest[i]);
   	}
   	buffer[length-1] = 0;
	
	MemFree(digest);
}

/**
 * Write some random data into a memory area
 * \param 	buffer	Place to write the random data to
 * \param	length	Number of bytes to write
 */
void 
XplRandomData(void *buffer, size_t length) 
{
	gcry_randomize(buffer, length, GCRY_STRONG_RANDOM);
}

/**
 * Save the current random seed to disk. This should only be done by 'management' processes, and
 * should not usually be called by agents.
 */
void
XplSaveRandomSeed(void)
{
	gcry_control (GCRYCTL_UPDATE_RANDOM_SEED_FILE);
}
