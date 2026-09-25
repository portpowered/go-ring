# process of transpiling one library into another library

## Problem statement
sometimes you just want to transpile from one language to another, because the original language
doesn't support some functioanlity or whatever or like its not portable.

## Solution
checklist
1. download the third party library
2. document the rough approximate architecture of the lbirary in terms of how it owkrs, the network protocols, and variations, level of abstraction
2. write out a mechanism into the third party library to run replay tests against it locally and use a standardized set of replay schemas, as well as denote teh coverage of the mechanism
3. write out a series of test plans that map deterministically from set of replay tests to the corresponding implementation.
4. implement tests until you have XX% coverage
5. define your new code as a plan in terms of context of best pratcies of your language and in alignment with the contextual iinformation of the library.
6. implement the code in correpsondence to those tests and the corresponding rough approximation of best practices and shape
7. you are finished when all the tests pass.
