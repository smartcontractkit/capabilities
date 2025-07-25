// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.0;

contract MessageEmitter {
    event MessageEmitted(string message);

    function emitMessage(string calldata message) external {
        emit MessageEmitted(message);
    }
}

