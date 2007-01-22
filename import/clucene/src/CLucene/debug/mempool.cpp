/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"

#ifdef LUCENE_ENABLE_MEMORY_POOL
	//classes for use in the memory pool definitions:
	#include "CLucene/index/TermInfo.h"
	#include "CLucene/search/SearchHeader.h"
	#include "CLucene/analysis/Analyzers.h"

   #define DEFINE_MP(ns,cls) lucene::debug::pool<> CL_NS(ns):: cls ##_mempool(sizeof(CL_NS(ns)::cls));
      
   //define the implementations for the mempools
   DEFINE_MP(index,TermInfo);
   DEFINE_MP(search,HitDoc);
   DEFINE_MP(analysis,Token);

CL_NS_DEF(debug)

   #ifdef LUCENE_ENABLE_MEMLEAKTRACKING
      #error "Cannot use memory pool and LuceneBase memory leak counting together"
   #endif
   #define RELEASE_MP(ns,cls) CL_NS(ns)::cls ## _mempool.release_memory()

   //now the list of mempools to release
   void lucene_release_memory_pool(){
      RELEASE_MP(index,TermInfo);  
      RELEASE_MP(search,HitDoc);  
      RELEASE_MP(analysis,Token);  
   }

CL_NS_END
#endif
