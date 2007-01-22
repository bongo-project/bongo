/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_debug_mem_h
#define _lucene_debug_mem_h

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "lucenebase.h"

//some memory pool definitions
#ifdef LUCENE_ENABLE_MEMORY_POOL
 #define LUCENE_MP_DEFINE(cls) extern lucene::debug::pool<> cls ## _mempool
#else
 #define LUCENE_MP_DEFINE(cls)
#endif

//Macro for creating new objects
//todo: currently mempool and lucenebase are exclusive of each other
//this doesnt need to be the case...
#if defined(LUCENE_ENABLE_MEMORY_POOL)
   #define _CLNEW new
   #define LUCENE_BASE public CL_NS(debug)::LuceneVoidBase
   #define LUCENE_POOLEDBASE(sc) public CL_NS(debug)::LucenePoolBase<&sc ## _mempool>
#elif defined(LUCENE_ENABLE_MEMLEAKTRACKING)
   #define _CLNEW new(__FILE__, __LINE__)
   #define LUCENE_BASE public CL_NS(debug)::LuceneBase
   #define LUCENE_POOLEDBASE(sc) LUCENE_BASE
#elif defined(LUCENE_ENABLE_REFCOUNT)
   #define _CLNEW new
   #define LUCENE_BASE public CL_NS(debug)::LuceneBase
   #define LUCENE_POOLEDBASE(sc) LUCENE_BASE
#else
   #define _CLNEW new
   #define LUCENE_BASE public CL_NS(debug)::LuceneVoidBase
   #define LUCENE_POOLEDBASE(sc) LUCENE_BASE
   #define LUCENE_BASE_CHECK(obj) (obj)->dummy__see_mem_h_for_details
#endif
#define _CL_POINTER(x) (x==NULL?NULL:(x->__cl_addref()>=0?x:x)) //return a add-ref'd object
#define LUCENE_REFBASE public CL_NS(debug)::LuceneBase //this is the base of classes who *always* need refcounting

#if defined(_DEBUG)
  #if !defined(LUCENE_BASE_CHECK)
		#define LUCENE_BASE_CHECK(x)
	#endif
#else
	#undef LUCENE_BASE_CHECK
	#define LUCENE_BASE_CHECK(x)
#endif

//Macro for creating new arrays
#ifdef LUCENE_ENABLE_MEMLEAKTRACKING
	#define _CL_NEWARRAY(type,size) (type*)CL_NS(debug)::LuceneBase::__cl_voidpadd(new type[(size_t)size],__FILE__,__LINE__,(size_t)size);
	#define _CLDELETE_ARRAY(x) if (x!=NULL){CL_NS(debug)::LuceneBase::__cl_voidpremove((const void*)x,__FILE__,__LINE__); delete [] x; x=NULL;}
	#define _CLDELETE_LARRAY(x) if (x!=NULL){CL_NS(debug)::LuceneBase::__cl_voidpremove((const void*)x,__FILE__,__LINE__);delete [] x;}
	#ifndef _CLDELETE_CARRAY
		#define _CLDELETE_CARRAY(x) if (x!=NULL){CL_NS(debug)::LuceneBase::__cl_voidpremove((const void*)x,__FILE__,__LINE__);delete [] x; x=NULL;}
		#define _CLDELETE_LCARRAY(x) if (x!=NULL){CL_NS(debug)::LuceneBase::__cl_voidpremove((const void*)x,__FILE__,__LINE__);delete [] x;}
	#endif
#else
	#define _CL_NEWARRAY(type,size) new type[size]
	#define _CLDELETE_ARRAY(x) if (x!=NULL){delete [] x; x=NULL;}
	#define _CLDELETE_LARRAY(x) if (x!=NULL){delete [] x;}
	#ifndef _CLDELETE_CARRAY
		#define _CLDELETE_CARRAY(x) if (x!=NULL){delete [] x; x=NULL;}
		#define _CLDELETE_LCARRAY(x) if (x!=NULL){delete [] x;}
	#endif
#endif
//a shortcut for deleting a carray and all its contents
#define _CLDELETE_CARRAY_ALL(x) {if ( x!=NULL ){ for(int xcda=0;x[xcda]!=NULL;xcda++)_CLDELETE_CARRAY(x[xcda]);}_CLDELETE_ARRAY(x)};
#ifndef _CLDELETE_CaARRAY
		#define _CLDELETE_CaARRAY _CLDELETE_CARRAY
		#define _CLDELETE_LCaARRAY _CLDELETE_LCARRAY
#endif

//Macro for deleting
#ifdef LUCENE_ENABLE_REFCOUNT
	#define _CLDELETE(x) if (x!=NULL){ CND_PRECONDITION((x)->__cl_refcount>=0,"__cl_refcount was < 0"); if ((x)->__cl_decref() <= 0)delete x; x=NULL; }
	#define _CLLDELETE(x) if (x!=NULL){ CND_PRECONDITION((x)->__cl_refcount>=0,"__cl_refcount was < 0"); if ((x)->__cl_decref() <= 0)delete x; }
#else
	#define _CLDELETE(x) if (x!=NULL){ LUCENE_BASE_CHECK(x); delete x; x=NULL; }
	#define _CLLDELETE(x) if (x!=NULL){ LUCENE_BASE_CHECK(x); delete x; }
#endif

//_CLDECDELETE deletes objects which are *always* refcounted
#define _CLDECDELETE(x) if (x!=NULL){ CND_PRECONDITION((x)->__cl_refcount>=0,"__cl_refcount was < 0"); if ((x)->__cl_decref() <= 0)delete x; x=NULL; }
#define _CLLDECDELETE(x) if (x!=NULL){ CND_PRECONDITION((x)->__cl_refcount>=0,"__cl_refcount was < 0"); if ((x)->__cl_decref() <= 0)delete x; }

//_VDelete should be used for deleting non-clucene objects.
//when using reference counting, _CLDELETE casts the object
//into a LuceneBase*.
#define _CLVDELETE(x) if(x!=NULL){delete x; x=NULL;}

#endif //_lucene_debug_lucenebase_
