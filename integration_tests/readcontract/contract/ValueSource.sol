// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.0;

contract ValueSource {
    event AddressLogged(address indexed addr);
    event UintLogged(uint indexed value);
    event IntLogged(int indexed value);
    event BoolLogged(bool value);
    event StringLogged(string value);
    event BytesLogged(bytes value);
    event Bytes32Logged(bytes32 value);


    function GetValue(
        address[] memory addresses,
        uint[] memory uints,
        int[] memory ints,
        bool[] memory bools,
        string[] memory strings,
        bytes[] memory bytesArray,
        bytes32[] memory bytes32Array
    ) public returns (int[] memory) {
        for (uint i = 0; i < addresses.length; i++) {
            emit AddressLogged(addresses[i]);
        }
        for (uint i = 0; i < uints.length; i++) {
            emit UintLogged(uints[i]);
        }
        for (uint i = 0; i < ints.length; i++) {
            emit IntLogged(ints[i]);
        }
        for (uint i = 0; i < bools.length; i++) {
            emit BoolLogged(bools[i]);
        }
        for (uint i = 0; i < strings.length; i++) {
            emit StringLogged(strings[i]);
        }
        for (uint i = 0; i < bytesArray.length; i++) {
            emit BytesLogged(bytesArray[i]);
        }
        for (uint i = 0; i < bytes32Array.length; i++) {
            emit Bytes32Logged(bytes32Array[i]);
        }


        int[] memory values = new int[](3);
        values[0] = 21;
        values[1] = 42;
        values[2] = 63;
        return values;
    }
}
