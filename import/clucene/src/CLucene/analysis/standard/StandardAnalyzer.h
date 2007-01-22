/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_analysis_standard_StandardAnalyzer
#define _lucene_analysis_standard_StandardAnalyzer

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/util/VoidMap.h"
#include "CLucene/util/Reader.h"
#include "CLucene/analysis/AnalysisHeader.h"
#include "CLucene/analysis/Analyzers.h"
#include "StandardFilter.h"
#include "StandardTokenizer.h"


CL_NS_DEF2(analysis,standard)

	//Represents a standard analyzer.
	class StandardAnalyzer : public Analyzer 
	{
	private:
		CL_NS(util)::CLSetList<TCHAR*> stopSet;
	public:
		// <summary> Builds an analyzer. </summary>
		StandardAnalyzer();

		//<summary> Builds an analyzer with the given stop words. </summary>
		StandardAnalyzer( TCHAR** stopWords);

		~StandardAnalyzer();


		// <summary>
		// Constructs a StandardTokenizer filtered by a 
		// StandardFilter, a LowerCaseFilter and a StopFilter.
		// </summary>
		TokenStream* tokenStream(const TCHAR* fieldName, CL_NS(util)::Reader* reader) 
		;
	};
CL_NS_END2
#endif
